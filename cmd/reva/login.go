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
	"bufio"
	"context"
	"fmt"
	"os"

	authregistryv0alphapb "github.com/cs3org/go-cs3apis/cs3/authregistry/v0alpha"
	gatewayv0alphapb "github.com/cs3org/go-cs3apis/cs3/gateway/v0alpha"
	rpcpb "github.com/cs3org/go-cs3apis/cs3/rpc"
)

var loginCommand = func() *command {
	cmd := newCommand("login")
	cmd.Description = func() string { return "login into the reva server" }
	cmd.Usage = func() string { return "Usage: login <type>" }
	listFlag := cmd.Bool("list", false, "list available login methods")
	cmd.Action = func() error {
		if *listFlag {
			// list available login methods
			client, err := getClient()
			if err != nil {
				return err
			}

			req := &authregistryv0alphapb.ListAuthProvidersRequest{}

			ctx := context.Background()
			res, err := client.ListAuthProviders(ctx, req)
			if err != nil {
				return err
			}

			if res.Status.Code != rpcpb.Code_CODE_OK {
				return formatError(res.Status)
			}

			fmt.Println("Available login methods:")
			for _, v := range res.Types {
				fmt.Printf("- %s\n", v)
			}
			return nil
		}

		var authType, username, password string
		if cmd.NArg() != 1 {
			fmt.Println(cmd.Usage())
			os.Exit(1)
		} else {
			authType = cmd.Args()[0]
			reader := bufio.NewReader(os.Stdin)
			fmt.Print("username: ")
			usernameInput, err := read(reader)
			if err != nil {
				return err
			}

			fmt.Print("password: ")
			passwordInput, err := readPassword(0)
			if err != nil {
				return err
			}

			username = usernameInput
			password = passwordInput
		}

		client, err := getClient()
		if err != nil {
			return err
		}

		req := &gatewayv0alphapb.AuthenticateRequest{
			Type:         authType,
			ClientId:     username,
			ClientSecret: password,
		}

		ctx := context.Background()
		res, err := client.Authenticate(ctx, req)
		if err != nil {
			return err
		}

		if res.Status.Code != rpcpb.Code_CODE_OK {
			return formatError(res.Status)
		}

		writeToken(res.Token)
		fmt.Println("OK")
		return nil
	}
	return cmd
}
