package main

// #include "phpkd.h"
import "C"
import "fmt"

//export gofunc
func gofunc(x C.int) {
    fmt.Printf("in gofunc with %d\n", x)
}

func main() {
    fmt.Printf("in go main func\n")
    C.phpkd_init()
    C.phpkd_request()
}
