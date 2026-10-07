// Package toolchain guards the repository's single Go version source: the
// toolchain line in go.mod. Its tests fail when a Dockerfile or a CI
// workflow pins a Go version of its own.
package toolchain
