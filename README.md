# dtbench: dnstap benchmark

Tool for benchmarking dnstap parsers by sending dnstap packets as fast as
possible to a unix socket where the parser is expected to listen.

Given that a dnstap parser is listening to `/tmp/parser.sock` example usage
would look like this:
```text
dtbench -u /tmp/parser.sock -n 4000000 -q "testing.example.com." -r
```
