package main

// c-shared requires an entry point; keep it independent of platform/cgo exports
// so repository-wide cross-compilation can also check the portable helpers.
func main() {}
