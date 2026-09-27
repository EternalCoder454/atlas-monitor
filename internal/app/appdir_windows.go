package app

import "os"

// localAppData is %LocalAppData%, where a Windows program puts data it wrote
// itself — as opposed to %AppData%, which roams with the user between machines.
// Atlas's state here is a source checkout path and an update log, both of which
// describe this machine and should not follow anyone anywhere.
func localAppData() string { return os.Getenv("LOCALAPPDATA") }
