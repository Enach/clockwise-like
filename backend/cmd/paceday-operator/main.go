// Command paceday-operator runs operator procedures on the server. It has no
// HTTP surface: only the deployment's operator, with direct access to the
// server and its database, can run it. Every mutating run is audited
// (operator_audit).
//
//	paceday-operator encryption status|convert|rotate-data-key|decrypt-all|reset-lost-key|downgrade-schema [flags]
//
// (PAC-84, contracts/features/PAC-84.md section 8.) DATABASE_URL names the
// database; migrations are applied first, as the server does on start.
package main

import (
	"fmt"
	"os"
)

const usage = "usage: paceday-operator encryption <subcommand> [flags] (see docs/runbook.md, \"Encryption key\")"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "encryption" {
		os.Exit(runEncryption(os.Args[2:], os.Getenv, os.Stdout, os.Stderr))
	}
	fmt.Fprintln(os.Stderr, usage)
	os.Exit(2)
}
