package sysfs

import (
	"errors"
	"io"
)

// readAt re-reads the file from offset zero.
//
// Windows has no pread; os.File.ReadAt is the positional read, and it reports
// io.EOF whenever it returns fewer bytes than asked for — which for these files
// is every single time, since they are read into a buffer deliberately larger
// than their contents. That is a short read, not a failure, so it is reported as
// success and the caller looks at n.
func (f *File) readAt(b []byte) (int, error) {
	n, err := f.f.ReadAt(b, 0)
	if errors.Is(err, io.EOF) {
		err = nil
	}
	return n, err
}
