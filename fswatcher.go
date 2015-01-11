package rfsnotify

// FSWatcher matches the fsnotify v1 interface.
type FSWatcher interface {
	NewWatcher() (*FSWatcher, error)
	Add(name string) error
	Remove(name string) error
	Close() error
}
