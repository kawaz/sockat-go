# Use SEQPACKET

> English | [日本語](./seqpacket-ja.md)

Part of the [user manual](../MANUAL.md).

sockat has no functions of its own for SEQPACKET. Hand a path made by [resolve](resolve.md) to a SEQPACKET socket you make yourself (`net.ListenUnix("unixpacket", ...)`, `net.DialUnix("unixpacket", ...)`). It is used as for stream: just change `"unix"` to `"unixpacket"`.

- The listening (binding) side: hand the path made by `ResolveForBind` to `net.ListenUnix("unixpacket", ...)` ([Let another library bind](resolve.md#let-another-library-bind))
- The connecting side: hand the path made by `ResolveForConnect` to `net.DialUnix("unixpacket", ...)` ([Connect many times](resolve.md#connect-many-times))

On macOS, the standard library cannot make SEQPACKET sockets. On FreeBSD, `Write` (write(2)) sets no record boundary, and what is sent in a row arrives as one record. To send each as a record of its own, send with `MSG_EOR` through `SyscallConn`, as in `unix.Sendmsg(fd, b, nil, nil, unix.MSG_EOR)`.
