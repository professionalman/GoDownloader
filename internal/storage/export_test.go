package storage

import "os"

// SetRenameFuncForTest allows tests to inject a simulated os.Rename implementation (e.g. EXDEV).
func SetRenameFuncForTest(fn func(string, string) error) func() {
	orig := renameFunc
	if fn == nil {
		renameFunc = os.Rename
	} else {
		renameFunc = fn
	}
	return func() {
		renameFunc = orig
	}
}

// SetRemoveAllFuncForTest allows tests to inject a simulated os.RemoveAll implementation.
func SetRemoveAllFuncForTest(fn func(string) error) func() {
	orig := removeAllFunc
	if fn == nil {
		removeAllFunc = os.RemoveAll
	} else {
		removeAllFunc = fn
	}
	return func() {
		removeAllFunc = orig
	}
}
