# rfsnotify v0.0.1
recursive directory notifications built as a wrapper around fsnotify (golang)

[![GoDoc](https://godoc.org/github.com/dietsche/rfsnotify?status.svg)](https://godoc.org/github.com/dietsche/rfsnotify)

This is a thin wrapper around https://github.com/go-fsnotify/fsnotify instead of only monitoring a top level folder,
it allows you to monitor all folders underneath the folder you specify.

Example:
--------

```
import "github.com/dietsche/rfsnotify"
watcher, err := rfsnotify.New("/tmp/")
 //from this point forward, refer to the fsnotify documentation
 
```
