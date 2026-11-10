package app

// Version is the build's version string, shown in the About dialog.
//
// It is a variable, not a constant, so the linker can set it:
//
//	go build -ldflags "-X link-chat/pkg/app.Version=1.2.0"
//
// A build that does not set it reports "dev", which is honest: a binary built
// from a working tree is not a release.
var Version = "dev"
