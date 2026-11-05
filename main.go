// sap-help — MCP-сервер для поиска и чтения help.sap.com и SAP Community.
//
// Один бинарник без зависимостей: только стандартная библиотека.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
)

const (
	serverName    = "sap-help"
	serverVersion = "0.2.1"
)

func main() {
	showVersion := flag.Bool("version", false, "напечатать версию и выйти")
	flag.Parse()
	if *showVersion {
		fmt.Println(serverName, serverVersion)
		return
	}

	log.SetOutput(os.Stderr) // stdout принадлежит протоколу
	log.SetFlags(log.LstdFlags)

	cl := newClient()
	ts := newToolset(cl)
	log.Printf("старт %s %s: %s, %s, только чтение (GET), кэш %s",
		serverName, serverVersion, cl.helpBase, cl.communityBase, cacheTTL)
	serve(os.Stdin, os.Stdout, ts)
}
