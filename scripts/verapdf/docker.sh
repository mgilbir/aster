#!/bin/sh
# docker.sh: veraPDF's command line, from the veraPDF Consortium's image
# (verapdf/cli, pinned by digest), for ASTER_VERAPDF:
#
#   ASTER_VERAPDF=$PWD/scripts/verapdf/docker.sh go test -run VeraPDF .
#
# The arguments are verapdf's; the last is the absolute path of the directory
# to validate, which the container sees read-only at the same path, with no
# network.
set -eu
image=verapdf/cli@sha256:d74b7026c18a0f171e73c153b617fe6520e1511feb24ca98a348b2bb897669fb # veraPDF 1.30.3
for dir; do :; done
case $dir in
/*) ;;
*) echo "docker.sh: the last argument must be an absolute directory, got $dir" >&2; exit 2 ;;
esac
exec docker run --rm --network none -v "$dir:$dir:ro" "$image" "$@"
