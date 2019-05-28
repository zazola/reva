// Copyright 2018-2019 CERN
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//
// In applying this license, CERN does not waive the privileges and immunities
// granted to it by virtue of its status as an Intergovernmental Organization
// or submit itself to any jurisdiction.

package main

import (
	"fmt"
	flag "github.com/spf13/pflag"
	"io/ioutil"
	"os"
	"os/exec"
	"path"
	"regexp"
	"strings"
)

var oses = []string{"darwin", "linux"}
var archs = []string{"amd64"}
var versionRegex = regexp.MustCompile(`^\d+\.\d+\.\d+$`)
var output = flag.String("output", "", "output dir for build artifacts, defaults to src dir")
var release = flag.String("release", "", "if set the output binaries will contain version in the name")

func die(val ...interface{}) {
	fmt.Fprintf(os.Stderr, "error: %+v\n", val)
	os.Exit(1)
}

func run(cmd string) {
	c := exec.Command("bash", "-c", cmd)
	c.Env = nil
	c.Stderr = os.Stderr

	if err := c.Run(); err != nil {
		die(err, cmd)
	}
}

func get(cmd string) string {
	out, err := exec.Command("bash", "-c", cmd).Output()
	if err != nil {
		die(err)
	}
	return strings.ReplaceAll(string(out), "\n", "")
}

type buildFlags struct {
	gitCommit  string
	gitDirty   string
	version    string
	goVersion  string
	goPlatform string
}

func getBuildFlags() *buildFlags {
	bf := &buildFlags{}
	bf.version = get(`git describe`)
	bf.goVersion = get(`go version | awk '{print $3}'`)
	bf.goPlatform = get(`go version | awk '{print $4}'`)
	bf.gitCommit = get(`git rev-parse --short HEAD`)
	bf.gitDirty = get(`git diff-index --quiet HEAD -- || echo "dirty-"`)
	return bf
}

func createBuildDir() string {
	d, err := ioutil.TempDir("", "reva")
	if err != nil {
		die(err)
	}
	return d
}

func createPackageDir(buildDir, os, arch, version string) string {
	d, err := ioutil.TempDir("", "reva")
	if err != nil {
		die(err)
	}
	return d
}

func getVersion() string {
	v := os.Args[1]
	if !versionRegex.MatchString(v) {
		die("version provided does not match format: <uint.uint.uint>")
	}
	return v
}

func getBinaryName(srcDir string, bf *buildFlags) string {
	if *release == "" { // dev builds
		return path.Base(srcDir)
	}
	return path.Base(srcDir) + "-" + *release
}

func build(os, arch string) {
	bf := getBuildFlags()
	srcDir := getSourceDir()                // ./cmd/reva/ or ./cmd/revad
	binaryName := getBinaryName(srcDir, bf) // reva or revad
	outDir := getOutDir(srcDir, binaryName)
	bs := getLDFlags(bf)
	run(fmt.Sprintf("go build -o %s -ldflags \"%s\" %s", outDir, bs, srcDir))
}

func getLDFlags(bf *buildFlags) string {
	return fmt.Sprintf("-s -X main.gitCommit=%s%s -X main.version=%s -X main.goVersion=%s -X main.buildPlatform=%s", bf.gitDirty, bf.gitCommit, bf.version, bf.goVersion, bf.goPlatform)
}

func getSourceDir() string {
	return os.Args[1]
}

func getOutDir(srcDir, binaryName string) string {
	if *output == "" {
		return path.Join(srcDir, binaryName)
	}
	return *output
}

func checkArgs() {
	if len(os.Args) < 2 {
		die("build <src_dir>")
	}
}

func main() {
	flag.Parse()
	checkArgs()
	for _, os := range oses {
		for _, arch := range archs {
			build(os, arch)
		}
	}
}
