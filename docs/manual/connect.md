# Connect

> English | [日本語](./connect-ja.md)

Part of the [user manual](../MANUAL.md). The code runs as Example functions in [example_connect_test.go](../../example_connect_test.go) (`path` is the socket's path, of any length).

## Connect

Write `sockat.Dial(path)` instead of `net.Dial("unix", path)`. It returns the standard `*net.UnixConn`.

```go
c, err := sockat.Dial(path) // *net.UnixConn
if err != nil {
	log.Fatal(err)
}
defer c.Close()
fmt.Fprintln(c, "ping")
```

## Connect from an HTTP client

Call `sockat.Dial` in the `DialContext` of an `http.Transport`. The host of the URL can be anything.

```go
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
```

## Connect to the same path many times

Make the short spelling once and use it again and again: use [resolve](resolve.md#connect-many-times).
