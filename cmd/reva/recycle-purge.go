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

	gatewayv0alphapb "github.com/cs3org/go-cs3apis/cs3/gateway/v0alpha"
	rpcpb "github.com/cs3org/go-cs3apis/cs3/rpc"
)

func recyclePurgeCommand() *command {
	cmd := newCommand("recycle-purge")
	cmd.Description = func() string { return "purge a recycle bin" }
	cmd.Usage = func() string { return "Usage: recycle-purge [-flags] " }

	cmd.Action = func() error {
		if cmd.NArg() < 0 {
			fmt.Println(cmd.Usage())
			os.Exit(1)
		}

		client, err := getClient()
		if err != nil {
			return err
		}

		req := &gatewayv0alphapb.PurgeRecycleRequest{}

		ctx := getAuthContext()
		res, err := client.PurgeRecycle(ctx, req)
		if err != nil {
			return err
		}

		if res.Status.Code != rpcpb.Code_CODE_OK {
			return formatError(res.Status)
		}

		return nil
	}
	return cmd
}
