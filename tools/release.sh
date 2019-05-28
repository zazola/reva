#!/bin/bash
set -xe  # Exit on error; debugging enabled.
set -o pipefail # Fail a pipe if any sub-command fails.

VERSION=v$1

git checkout master
calens > CHANGELOG.md
git add CHANGELOG.md
git commit -m "changelog: update for version ${VERSION}"
git tag -s -a -m "${VERSION}" ${VERSION}
git archive --format=tar --prefix=reva-${VERSION}/ ${VERSION} | gzip -n > reva-${VERSION}.tar.gz

tmp=`mktemp -d`
mv reva-${VERSION}.tar.gz ${tmp}/reva-${VERSION}.tar.gz

current=${PWD}

cd ${tmp}
tar xz --strip-components=1 -f reva-${VERSION}.tar.gz

# run build in container, map 
docker run --volume "${tmp}:/reva" --volume "$PWD/releases:/output" -d cs3org/revabuilder

# check we have the files
find releases

cd ${current}
