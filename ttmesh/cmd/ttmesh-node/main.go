// main.go for ttmesh-node
package main

import (
    "os"
)

func main() {
    os.Exit(run(ParseFlags(os.Args[1:])))
}
