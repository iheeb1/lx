package example

import (
	"fmt"
	"testing"
)

func TestFirst(t *testing.T) {
	t.Error("first broke")
}

func Example_greeting() {
	fmt.Println("hello")
	fmt.Println("world")
	// Output:
	// hello
	// there
}

func TestAfter(t *testing.T) {
	fmt.Println("after chatter")
}

func Example_ok() {
	fmt.Println("fine")
	// Output: fine
}
