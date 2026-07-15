package bannedoutput

import (
	"fmt"
	"log"
	"os"
	"strings"
)

func bannedCalls() {
	// Direct fmt stdout writes — all banned.
	fmt.Printf("hello %s\n", "world")   // want `banned: fmt.Printf`
	fmt.Println("hello")                 // want `banned: fmt.Println`
	fmt.Print("hello")                   // want `banned: fmt.Print`

	// fmt.Fprint* to os.Stderr — banned.
	fmt.Fprintf(os.Stderr, "err %v\n", "x")  // want `banned: fmt.Fprintf`
	fmt.Fprintln(os.Stderr, "err")            // want `banned: fmt.Fprintln`
	fmt.Fprint(os.Stderr, "err")              // want `banned: fmt.Fprint`

	// fmt.Fprint* to os.Stdout — banned.
	fmt.Fprintf(os.Stdout, "out %v\n", "x")  // want `banned: fmt.Fprintf`
	fmt.Fprintln(os.Stdout, "out")            // want `banned: fmt.Fprintln`
	fmt.Fprint(os.Stdout, "out")              // want `banned: fmt.Fprint`

	// log.* — all banned.
	log.Printf("log %s\n", "x")   // want `banned: log.Printf`
	log.Println("log")             // want `banned: log.Println`
	log.Print("log")               // want `banned: log.Print`
	log.Fatalf("fatal %s\n", "x") // want `banned: log.Fatalf`
	log.Fatal("fatal")             // want `banned: log.Fatal`
	log.Panicf("panic %s\n", "x") // want `banned: log.Panicf`
	log.Panic("panic")             // want `banned: log.Panic`
}

func allowedCalls() {
	// fmt.Sprintf / Sprint / Sprintln / Errorf — NOT banned (no I/O).
	_ = fmt.Sprintf("hello %s", "world")
	_ = fmt.Sprint("hello")
	_ = fmt.Sprintln("hello")
	_ = fmt.Errorf("error: %w", fmt.Errorf("inner"))

	// fmt.Fprint* to a non-stdio writer — NOT banned.
	var buf strings.Builder
	fmt.Fprintf(&buf, "buffered %s\n", "x")
	fmt.Fprintln(&buf, "buffered")
	fmt.Fprint(&buf, "buffered")

	// Writing to an arbitrary io.Writer variable — NOT banned.
	var w = os.Stderr // the writer is held in a variable, not os.Stderr directly
	_ = w
}
