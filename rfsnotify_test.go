package rfsnotify

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

func ExampleRWatcher_AddRecursive() {
	// create test folder
	rd := filepath.Join(os.TempDir(), "testRWatcher_recursive")
	err := os.Mkdir(rd, 0o750)
	if err != nil {
		fmt.Printf("could not create root test folder, error: %s", err)
		return
	}
	defer func() {
		if e := os.RemoveAll(rd); e != nil {
			fmt.Printf("could not remove test folder %s, error: %s", rd, e)
		}
	}()
	if err = os.MkdirAll(filepath.Join(rd, "a/b/c"), 0o750); err != nil {
		fmt.Printf("could not create test folders, error: %s", err)
		return
	}

	// create new watcher
	watcher, err := NewWatcher()
	if err != nil {
		fmt.Printf("could not create recursive watcher, error: %s", err)
		return
	}

	defer func() {
		if e := watcher.Close(); e != nil {
			fmt.Printf("could not close recursive watcher, error: %s", e)
		}
	}()

	// add test folder to be watched
	err = watcher.AddRecursive(rd)
	if err != nil {
		fmt.Printf("could not add recursive folder %s, error: %s", rd, err)
		return
	}

	// create new monitored folder
	if err = os.MkdirAll(filepath.Join(rd, "a/b/c/d"), 0o750); err != nil {
		fmt.Printf("could not create test folders, error: %s", err)
		return
	}

	// dump folder creation event

	<-watcher.Events

	// add dummy event listener, real one must use a controlled loop
	wg := sync.WaitGroup{}
	wg.Go(func() {
		event, ok := <-watcher.Events
		if !ok {
			fmt.Printf("ko event")
			return
		}
		fmt.Printf("received event %s", event)
	})

	// create a test file
	f, err := os.Create(filepath.Clean(filepath.Join(rd, "a/b/c/d", "test.txt")))
	if err != nil {
		fmt.Printf("could not create test file %s, error: %s", rd, err)
		return
	}
	defer func() {
		if e := f.Close(); e != nil {
			fmt.Printf("could not close file %s, error: %s", f.Name(), e)
		}
	}()

	// wait until dummy listener is informed of the file creation
	wg.Wait()

	// cleanup
	if err = watcher.RemoveRecursive(rd); err != nil {
		fmt.Printf("could not remove test folder %s from notify, error: %s", rd, err)
		return
	}

	// output:
	// received event CREATE        "/tmp/testRWatcher_recursive/a/b/c/d/test.txt"
}

func ExampleRWatcher_Add() {
	// create test folder
	rd := filepath.Join(os.TempDir(), "testRWatcher_add")
	err := os.Mkdir(rd, 0o750)
	if err != nil {
		fmt.Printf("could not create root test folder, error: %s", err)
		return
	}
	defer func() {
		if e := os.RemoveAll(rd); e != nil {
			fmt.Printf("could not remove test folder %s, error: %s", rd, e)
		}
	}()
	if err = os.MkdirAll(filepath.Join(rd, "a/b/c"), 0o750); err != nil {
		fmt.Printf("could not create test folders, error: %s", err)
		return
	}

	// create new watcher
	watcher, err := NewWatcher()
	if err != nil {
		fmt.Printf("could not create recursive watcher, error: %s", err)
		return
	}

	defer func() {
		if e := watcher.Close(); e != nil {
			fmt.Printf("could not close recursive watcher, error: %s", e)
		}
	}()

	// add dummy event listener, real one must use a controlled loop
	wg := sync.WaitGroup{}
	wg.Go(func() {
		event, ok := <-watcher.Events
		if !ok {
			fmt.Printf("ko event")
			return
		}
		fmt.Printf("received event %s", event)
	})

	// add test folder to be watched
	err = watcher.Add(rd)
	if err != nil {
		fmt.Printf("could not add recursive folder %s, error: %s", rd, err)
		return
	}

	// create a test file that will not trigger notify
	f, err := os.Create(filepath.Clean(filepath.Join(rd, "a/b/c", "test.txt")))
	if err != nil {
		fmt.Printf("could not create test file %s, error: %s", rd, err)
		return
	}
	defer func() {
		if e := f.Close(); e != nil {
			fmt.Printf("could not close file %s, error: %s", f.Name(), e)
		}
	}()

	// create a test file that will trigger notify
	f2, err := os.Create(filepath.Clean(filepath.Join(rd, "test2.txt")))
	if err != nil {
		fmt.Printf("could not create test file2 %s, error: %s", rd, err)
		return
	}
	defer func() {
		if e := f2.Close(); e != nil {
			fmt.Printf("could not close file %s, error: %s", f2.Name(), e)
		}
	}()

	// wait until dummy listener is informed of the file creation
	wg.Wait()

	// cleanup
	if err = watcher.Remove(rd); err != nil {
		fmt.Printf("could not remove test folder %s from notify, error: %s", rd, err)
		return
	}

	// output:
	// received event CREATE        "/tmp/testRWatcher_add/test2.txt"
}
