package apply

import "syscall"

// syscallENOTDIR: lstat of "a/b" where "a" is a file.
var syscallENOTDIR error = syscall.ENOTDIR
