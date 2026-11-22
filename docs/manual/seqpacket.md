# Use SEQPACKET

> English | [日本語](./seqpacket-ja.md)

Part of the [user manual](../MANUAL.md).

sockat has no functions of its own for SEQPACKET. Hand a path made by [resolve](resolve.md) to a SEQPACKET socket you make yourself (`net.ListenUnix("unixpacket", ...)`, `net.DialUnix("unixpacket", ...)`). It is used as for stream: just change `"unix"` to `"unixpacket"`.

The standard library on macOS cannot make SEQPACKET sockets.
