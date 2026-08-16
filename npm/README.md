# hukou on npm

`hukou` is a household registry for stray binaries: find, adopt, upgrade, and
roll back the CLI tools no package manager owns. This npm package ships the
official release binary.

```sh
npm install -g @rtwsvj/hukou
hukou --help
```

## How it works

The `hukou` package is a thin wrapper: the real binaries live in optional
platform packages (`hukou-darwin-arm64`, `hukou-darwin-amd64`,
`hukou-linux-arm64`, `hukou-linux-amd64`), so npm only downloads the one your
machine needs. The binaries are byte-identical to the
[GitHub release](https://github.com/rtwsvj/hukou/releases) archives and carry
their SHA-256 checksums and Sigstore build-provenance attestations.

## Platform support

macOS (Apple Silicon / Intel) and Linux (arm64 / amd64). Windows is not
supported.

## Documentation

See the [repository](https://github.com/rtwsvj/hukou) for the full manual,
the CLI reference, and the data model.
