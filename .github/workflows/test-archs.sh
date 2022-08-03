#!/bin/sh
#
# Run tests on different architectures with QEMU. This will run tests for all
# architectures that the current GOOS supports.
#
# Pass any options to the test binary with -test.flag; for example:
#
#    % ./test-archs -test.v
#
# Explicitly set GOARCH to only run that one architecture:
#
#    % GOARCH=arm ./test-archs
#
# Please keep this script POSIX-compatible for maximum portability across
# different systems; people may want to run this on environments where bash is
# not available by default.
set -euC

# As GOARCH qemu-arch (they're not always identical)
#
# Not supported in QEMU yet (will be in 7.1):
# loong64   loongarch64
archs="
386        i386
arm        arm
arm64      aarch64
mips       mips
mips64     mips64
mips64le   mips64el
mipsle     mipsel
ppc64      ppc64
ppc64le    ppc64le
riscv64    riscv64
s390x      s390x
"

if [ "x${GOOS:-}" != "x" ]; then
	echo >&2 "$0: error: setting GOOS is not supported"
	exit 1
fi

IFS="
"
run_only=${GOARCH:-}
supported=" $(go tool dist list | grep "^$(go env GOOS)" | sed 's@.*/@@' | tr '\n' ' ')"

err=0
for a in $archs; do
	export GOARCH="${a%% *}"
	qemu="qemu-${a##* }"

	if ! echo "$supported" | grep -q " $GOARCH "; then
		continue
	fi
	if [ -n "$run_only" ] && [ "$run_only" != "$GOARCH" ]; then
		continue
	fi

	printf 'Testing %-10s (with %s)\n' "$GOARCH" "$qemu"

	if ! command -v "$qemu" >/dev/null; then
		echo >&2 "$0: error: command '$qemu' not on system"
		err=1
		continue
	fi

	for p in $(go list ./...); do
		printf '\t%s\n' "$p"

		go test -c "$p" -o "./$GOARCH.test"
		if [ -f "$GOARCH.test" ]; then  # Not all packages have tests.
			$qemu "./$GOARCH.test" "$@" || err=1
			rm "$GOARCH.test"
		fi
	done
done

exit $err
