// Package linkchat holds the repository-wide release version.
//
// It lives at the module root only because go:embed cannot reach files above a
// package's directory, and the VERSION file belongs at the top of the repo where
// people expect to find it. Read the version through pkg/version, not here.
package linkchat

import _ "embed"

// RawVersion is the untrimmed content of the VERSION file.
//
//go:embed VERSION
var RawVersion string
