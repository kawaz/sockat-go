package sockat_test

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"

	"github.com/kawaz/sockat-go"
)

// Dial takes a path of any length where net.Dial("unix", path) would, and
// returns the net package's own *net.UnixConn. The echo server here sends
// back what the client wrote, so the connection reached the long path.
func ExampleDial() {
	tmp, dir := longDir()
	defer os.RemoveAll(tmp)
	path := filepath.Join(dir, "server.sock")
	defer echoServer(path).Close()

	c, err := sockat.Dial(path) // *net.UnixConn
	if err != nil {
		log.Fatal(err)
	}
	defer c.Close()
	fmt.Fprintln(c, "ping")

	fmt.Println(readLine(c))
	// Output: ping
}

// An HTTP client reaches a server on a Unix socket through its transport's
// DialContext: dial the socket there, whatever the host of the URL. The
// handler's answer comes back through the long path.
func ExampleDial_http() {
	tmp, dir := longDir()
	defer os.RemoveAll(tmp)
	path := filepath.Join(dir, "server.sock")
	l, err := sockat.Listen(path)
	if err != nil {
		log.Fatal(err)
	}
	defer l.Close()
	go http.Serve(l, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "hello")
	}))

	client := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return sockat.Dial(path)
		},
	}}
	res, err := client.Get("http://unix/")
	if err != nil {
		log.Fatal(err)
	}
	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Print(string(body))
	// Output: hello
}

// httpGet gets "/" from the HTTP server on the socket at path, as
// ExampleDial_http does, and returns the body without its newline.
func httpGet(path string) string {
	client := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return sockat.Dial(path)
		},
	}}
	res, err := client.Get("http://unix/")
	if err != nil {
		log.Fatal(err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		log.Fatal(err)
	}
	return string(body[:len(body)-1])
}
