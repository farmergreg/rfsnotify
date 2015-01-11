package rfsnotify

// RFSWatcher supports recursive folder monitoring.
type RFSWatcher interface {
	FSWatcher
	AddRecursive(name string) error
}
