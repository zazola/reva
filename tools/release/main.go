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
	"io/ioutil"
	"os"
	"path"
	//	"bytes"
	"fmt"
	"os/exec"
	"strings"
)

func die(f string, args ...interface{}) {
	if !strings.HasSuffix(f, "\n") {
		f += "\n"
	}
	f = "\x1b[31m" + f + "\x1b[0m"
	fmt.Fprintf(os.Stderr, f, args...)
	os.Exit(1)
}

func msg(f string, args ...interface{}) {
	if !strings.HasSuffix(f, "\n") {
		f += "\n"
	}
	f = "\x1b[32m" + f + "\x1b[0m"
	fmt.Printf(f, args...)
}

func run(cmd string, args ...string) {
	c := exec.Command(cmd, args...)
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	runraw(c)
}

func runraw(c *exec.Cmd) {
	err := c.Run()
	if err != nil {
		die("error running command: %v %+v", c, err)
	}
}

func tmpdir() string {
	fn, err := ioutil.TempDir("", "reva-build")
	if err != nil {
		die("error creating tempdir: %+v", err)
	}
	return fn
}

func checkoutMaster() {
	run("git", "checkout", "master")
}

func updateChangelog() {
	fd, err := os.Create("CHANGELOG.md")
	if err != nil {
		die("error creating CHANGELOG.md: %+v", err)
	}
	defer fd.Close()
	c1 := exec.Command("calens")
	c1.Stdout = fd
	runraw(c1)
}

func extractTar(filename, outputDir string) {
	msg("extracting tar in output dir: %s", outputDir)
	c := exec.Command("tar", "xz", "--strip-components=1", "-f", filename)
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	c.Dir = outputDir
	runraw(c)
}

func main() {
	version := "v0.0.1"
	checkoutMaster()
	updateChangelog()
	run("git", "add", "CHANGELOG.md")
	run("git", "commit", "-m", "changelog: update for version "+version)
	run("git", "tag", "-s", "-a", "-m", version, version)
	run("bash", "-c", fmt.Sprintf("git archive --format=tar --prefix=reva-%s/ %s | gzip -n > reva-%s.tar.gz", version, version, version))
	tmp := tmpdir()
	filename := fmt.Sprintf("reva-%s.tar.gz", version)
	output := path.Join(tmp, filename)
	run(fmt.Sprintf("mv %s %s", filename, output))
	extractTar(filename, tmp)
}
