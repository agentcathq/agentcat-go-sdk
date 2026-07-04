// Package testutil provides common testing utilities shared across test files
package testutil

// StrPtr returns a pointer to the given string
func StrPtr(s string) *string {
	return &s
}

// IntPtr returns a pointer to the given int
func IntPtr(i int) *int {
	return &i
}

// Int32Ptr returns a pointer to the given int32
func Int32Ptr(i int32) *int32 {
	return &i
}

// BoolPtr returns a pointer to the given bool
func BoolPtr(b bool) *bool {
	return &b
}

// DerefStr safely dereferences a string pointer, returning the default value if nil
func DerefStr(p *string, def string) string {
	if p != nil {
		return *p
	}
	return def
}

// DerefInt safely dereferences an int pointer, returning the default value if nil
func DerefInt(p *int, def int) int {
	if p != nil {
		return *p
	}
	return def
}
