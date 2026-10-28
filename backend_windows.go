//go:build windows

package rfsnotify

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"unsafe"

	"github.com/fsnotify/fsnotify"
	"golang.org/x/sys/windows"
)

const (
	// bufferSize is the ReadDirectoryChangesW buffer size of a watch, the maximum supported by network shares.
	bufferSize = 64 * 1024
	// notifyFilter is the one used by fsnotify.
	notifyFilter = windows.FILE_NOTIFY_CHANGE_FILE_NAME | windows.FILE_NOTIFY_CHANGE_DIR_NAME | windows.FILE_NOTIFY_CHANGE_LAST_WRITE
)

func newBackend() (backend, <-chan fsnotify.Event, <-chan error, error) {
	b := &windowsBackend{
		events:  make(chan fsnotify.Event),
		errors:  make(chan error),
		done:    make(chan struct{}),
		watches: make(map[string]*dirWatch),
	}
	return b, b.events, b.errors, nil
}

// windowsBackend watches directories with ReadDirectoryChangesW, which natively watches whole trees:
// a recursive watch costs one handle and one buffer, whatever the number of sub-directories.
// fsnotify needs one per directory, i.e. gigabytes of buffers for large trees.
// Only directories can be watched.
type windowsBackend struct {
	events chan fsnotify.Event
	errors chan error
	done   chan struct{}
	wg     sync.WaitGroup

	mu      sync.Mutex
	watches map[string]*dirWatch
	closed  bool
}

type dirWatch struct {
	path      string
	recursive bool
	handle    windows.Handle
	ov        windows.Overlapped
	buf       []byte // read buffer, must not be touched while a read is pending

	mu      sync.Mutex
	stopped bool
}

func (b *windowsBackend) Add(name string) error {
	return b.addWatch(name, false)
}

func (b *windowsBackend) AddRecursive(name string) error {
	return b.addWatch(name, true)
}

func (b *windowsBackend) addWatch(name string, recursive bool) error {
	path := filepath.Clean(name)
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("could not watch %s: not a directory", path)
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return fsnotify.ErrClosed
	}
	if w, ok := b.watches[path]; ok {
		if w.recursive || !recursive {
			return nil
		}
		// replaced by a recursive watch
		delete(b.watches, path)
		w.stop()
	}

	handle, err := windows.CreateFile(windows.StringToUTF16Ptr(path), windows.FILE_LIST_DIRECTORY,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OVERLAPPED, 0)
	if err != nil {
		return &os.PathError{Op: "CreateFile", Path: path, Err: err}
	}
	event, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		_ = windows.CloseHandle(handle)
		return os.NewSyscallError("CreateEvent", err)
	}
	w := &dirWatch{
		path:      path,
		recursive: recursive,
		handle:    handle,
		ov:        windows.Overlapped{HEvent: event},
		buf:       make([]byte, bufferSize),
	}
	// first read is pending before returning, so that no change is missed after Add
	if err := w.read(); err != nil {
		w.release()
		return &os.PathError{Op: "ReadDirectoryChanges", Path: path, Err: err}
	}
	b.watches[path] = w
	b.wg.Go(func() {
		b.run(w)
	})
	return nil
}

func (b *windowsBackend) Remove(name string) error {
	path := filepath.Clean(name)
	b.mu.Lock()
	defer b.mu.Unlock()
	w, ok := b.watches[path]
	if !ok {
		return fmt.Errorf("%w: %s", fsnotify.ErrNonExistentWatch, path)
	}
	delete(b.watches, path)
	w.stop()
	return nil
}

func (b *windowsBackend) WatchList() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	list := make([]string, 0, len(b.watches))
	for path := range b.watches {
		list = append(list, path)
	}
	return list
}

func (b *windowsBackend) Close() error {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil
	}
	b.closed = true
	for path, w := range b.watches {
		delete(b.watches, path)
		w.stop()
	}
	b.mu.Unlock()
	close(b.done)
	b.wg.Wait()
	close(b.events)
	close(b.errors)
	return nil
}

// forget drops w from the watch list, unless it has been replaced already.
func (b *windowsBackend) forget(w *dirWatch) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.watches[w.path] == w {
		delete(b.watches, w.path)
	}
}

// run waits for w reads to complete and sends their events, until w is stopped.
func (b *windowsBackend) run(w *dirWatch) {
	defer w.release()
	spare := make([]byte, bufferSize)
	for {
		var n uint32
		err := windows.GetOverlappedResult(w.handle, &w.ov, &n, true)
		switch {
		case errors.Is(err, windows.ERROR_OPERATION_ABORTED):
			// stopped
			return
		case errors.Is(err, windows.ERROR_ACCESS_DENIED):
			// watched directory removed
			b.forget(w)
			b.sendEvent(fsnotify.Event{Name: w.path, Op: fsnotify.Remove})
			return
		case errors.Is(err, windows.ERROR_NOTIFY_ENUM_DIR):
			n = 0
		case err != nil:
			b.forget(w)
			b.sendError(fmt.Errorf("could not read changes of %s: %w", w.path, os.NewSyscallError("GetOverlappedResult", err)))
			return
		}

		// read again before sending events, so that the system keeps recording changes meanwhile
		data := w.buf
		w.buf = spare
		rearmed, err := w.readAgain()
		if !rearmed {
			return
		}
		if n == 0 {
			b.sendError(fmt.Errorf("%w: %s", fsnotify.ErrEventOverflow, w.path))
		} else {
			b.sendEvents(w.path, data[:n])
		}
		spare = data
		if err != nil {
			b.forget(w)
			b.sendError(fmt.Errorf("could not read changes of %s: %w", w.path, os.NewSyscallError("ReadDirectoryChanges", err)))
			return
		}
	}
}

func (b *windowsBackend) sendEvents(root string, data []byte) {
	var offset uint32
	for {
		raw := (*windows.FileNotifyInformation)(unsafe.Pointer(&data[offset]))
		name := windows.UTF16ToString(unsafe.Slice(&raw.FileName, raw.FileNameLength/2))
		e := fsnotify.Event{Name: filepath.Join(root, name)}
		switch raw.Action {
		case windows.FILE_ACTION_ADDED, windows.FILE_ACTION_RENAMED_NEW_NAME:
			e.Op = fsnotify.Create
		case windows.FILE_ACTION_REMOVED:
			e.Op = fsnotify.Remove
		case windows.FILE_ACTION_MODIFIED:
			e.Op = fsnotify.Write
		case windows.FILE_ACTION_RENAMED_OLD_NAME:
			e.Op = fsnotify.Rename
		}
		if e.Op != 0 && !b.sendEvent(e) {
			return
		}
		if raw.NextEntryOffset == 0 {
			return
		}
		offset += raw.NextEntryOffset
		if offset >= uint32(len(data)) {
			b.sendError(fmt.Errorf("invalid change notification for %s, events have likely been missed", root))
			return
		}
	}
}

func (b *windowsBackend) sendEvent(e fsnotify.Event) bool {
	select {
	case b.events <- e:
		return true
	case <-b.done:
		return false
	}
}

func (b *windowsBackend) sendError(err error) {
	select {
	case b.errors <- err:
	case <-b.done:
	}
}

func (w *dirWatch) read() error {
	w.ov = windows.Overlapped{HEvent: w.ov.HEvent}
	return windows.ReadDirectoryChanges(w.handle, &w.buf[0], uint32(len(w.buf)), w.recursive, notifyFilter, nil, &w.ov, 0)
}

// readAgain starts the next read, unless w is stopped: rearmed is false then.
// Checking and reading under w.mu ensures that stop cancels any read started.
func (w *dirWatch) readAgain() (rearmed bool, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.stopped {
		return false, nil
	}
	return true, w.read()
}

// stop cancels the pending read, if any, run then returns.
func (w *dirWatch) stop() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.stopped = true
	// fails if no read is pending, run will see w.stopped
	_ = windows.CancelIoEx(w.handle, &w.ov)
}

func (w *dirWatch) release() {
	_ = windows.CloseHandle(w.handle)
	_ = windows.CloseHandle(w.ov.HEvent)
}
