package gitrepo

import "os"

func writeDir(p string) error { return os.MkdirAll(p, 0o700) }
