// SPDX-License-Identifier: GPL-3.0-or-later
// Port of synccit by James Wilson / drakeapps (https://github.com/drakeapps/synccit), GPL-3.0-or-later.

// Command synccit runs the synccit API server (default) and its user/device
// administration CLI.
package main

import (
	"os"

	"github.com/rezhajulio/synccit-go/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
