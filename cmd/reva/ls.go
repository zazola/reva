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
	"os"
	"path"

	rpcpb "github.com/cs3org/go-cs3apis/cs3/rpc"
	storageproviderv0alphapb "github.com/cs3org/go-cs3apis/cs3/storageprovider/v0alpha"
)

func lsCommand() *command {
	cmd := newCommand("ls")
	cmd.Description = func() string { return "list a container contents" }
	cmd.Usage = func() string { return "Usage: ls [-flags] <container_name>" }
	longFlag := cmd.Bool("l", false, "long listing")
	fullFlag := cmd.Bool("f", false, "shows full path")

	cmd.Action = func() error {
		if cmd.NArg() < 1 {
			fmt.Println(cmd.Usage())
			os.Exit(1)
		}

		fn := cmd.Args()[0]
		client, err := getClient()
		if err != nil {
			return err
		}

		ref := &storageproviderv0alphapb.Reference{
			Spec: &storageproviderv0alphapb.Reference_Path{Path: fn},
		}
		req := &storageproviderv0alphapb.ListContainerRequest{Ref: ref}

		ctx := getAuthContext()
		res, err := client.ListContainer(ctx, req)
		if err != nil {
			return err
		}

		if res.Status.Code != rpcpb.Code_CODE_OK {
			return formatError(res.Status)
		}

		infos := res.Infos
		for _, info := range infos {
			p := info.Path
			if !*fullFlag {
				p = path.Base(info.Path)
			}

			if *longFlag {
				fmt.Printf("%+v %d %d %v %s\n", info.PermissionSet, info.Mtime, info.Size, info.Id, p)
			} else {
				fmt.Println(p)
			}
		}
		return nil
	}
	return cmd
}
