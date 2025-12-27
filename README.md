# rfsnotify

recursive directory notifications built as a wrapper around fsnotify (golang), forked from [this repo](https://github.com/farmergreg/rfsnotify)

[![GoDoc](https://godoc.org/github.com/farmergreg/rfsnotify?status.svg)](https://godoc.org/github.com/farmergreg/rfsnotify)

This is a thin wrapper around https://github.com/fsnotify/fsnotify instead of only monitoring a top level folder,
it allows you to monitor all folders underneath the folder you specify.

Example:
--------
(error handling omitted to improve readability)
```
    import "github.com/farmergreg/rfsnotify"

//rfsnotify works exactly like fsnotify and implements the same API.
    watcher, err := rfsnotify.NewWatcher()

//rfsnotify adds two new API entry points:
    watcher.AddRecursive("/tmp/")
    watcher.RemoveRecursive("/tmp/")
```
