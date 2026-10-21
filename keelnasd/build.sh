#!/bin/sh
# Build keelnasd for keelNAS (FreeBSD amd64, static, no cgo) into keelnasd/keelnasd; vet and test first.
#   keelnasd/build.sh            then copy keelnasd to tools/image/files/keelnasd for an image build
set -e
cd "$(dirname "$0")"
GO=${GO:-$(command -v go || echo "$HOME/.local/go/bin/go")}
V=${VERSION:-$(git describe --always --dirty 2>/dev/null || echo dev)}
# The page keel shows (internal/nas/page/page.js, docs/04 section 16) is signed with the package
# key, so that keel can tell it is ours; without the key (KEY, default the control machine's)
# the build has no signature and keel shows its built-in page.
KEY=${KEY:-$HOME/.keelos/sign/kimg-ed25519.pem}
P=internal/nas/page/page.js
rm -f $P.sig
if [ -f "$KEY" ]; then
	openssl pkeyutl -sign -inkey "$KEY" -rawin -in $P -out $P.sig
	echo "page.js signed"
else
	echo "no signing key $KEY: page.js unsigned" >&2
fi
$GO vet ./...
$GO test ./...
GOOS=freebsd GOARCH=amd64 CGO_ENABLED=0 $GO build -trimpath -ldflags "-s -w -X main.version=$V" -o keelnasd ./cmd/keelnasd
echo "keelnasd $V: $(ls -l keelnasd | awk '{print $5}') bytes"
