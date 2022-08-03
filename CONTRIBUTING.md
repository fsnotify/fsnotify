Thank you for your interest in contributing to fsnotify! We try to review and
merge PRs in a reasonable timeframe, but please be aware that:

- To avoid "wasted" work, please discus changes on the issue tracker first. You
  can just send PRs, but they may end up being rejected for one reason or the
  other.

- fsnotify is a cross-platform library, and changes must work reasonably well on
  all supported platforms.

- Changes will need to be compatible; old code should still compile, and the
  runtime behaviour can't change in ways that are likely to lead to problems for
  users.

Contributor License Agreement
-----------------------------
fsnotify is derived from code in the [golang.org/x/exp] package and it may be
merged back to a golang.org/x/... package in the future. Therefore fsnotify
carries the same license as Go and all contributors have to [sign the Google
CLA]. **You retain your copyright**; it just declares that you own the copyright
(i.e. wrote the code) and grants Google permission to use it.

[golang.org/x/exp]: https://godoc.org/golang.org/x/exp
[sign the Google CLA]: https://cla.developers.google.com/about/google-individual

Please indicate that you have signed the CLA in your pull request.

Testing
-------
Just `go test ./...` runs all the tests; the CI runs this on all supported
platforms. Testing different platforms locally can be done with something like
[goon] or [Vagrant], but this isn't super-easy to set up at the moment.

Running tests on different architectures with QEMU can be done with the
`.github/workflows/test-archs.sh` script; this uses `qemu-arch` and doesn't
require setting up a full VM. This is run in the CI for Linux only at the
moment.

The main tests are in [integration_test.go].

[goon]: https://github.com/arp242/goon
[Vagrant]: https://www.vagrantup.com/
[integration_test.go]: /integration_test.go
