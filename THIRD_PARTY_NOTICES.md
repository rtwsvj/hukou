# Third-party notices

hukou is licensed under the Apache License 2.0. See [LICENSE](LICENSE).
Third-party components and adapted code remain under their respective
licenses; the hukou license does not replace or narrow those terms.

## Adapted source code

### eget

- Project: <https://github.com/zyedidia/eget>
- Evaluated commit: `0983dea`
- License: MIT
- Copyright: Copyright (c) 2021 Zachary Yedidia
- Local use: `internal/assetpick/detect.go` is adapted from `detect.go`
- License text: [LICENSES/eget-MIT.txt](LICENSES/eget-MIT.txt)

The adapted file retains its source, copyright, license, and modification
notice.

### gup

- Project: <https://github.com/nao1215/gup>
- Evaluated commit: `952fb83`
- License: Apache License 2.0
- Copyright: Copyright 2022 CHIKAMATSU Naohiro
- Local use: `internal/provenance/gobin.go` is adapted from
  `internal/goutil/pkginfo.go`
- License text: [LICENSES/gup-APACHE-2.0.txt](LICENSES/gup-APACHE-2.0.txt)

The adapted file retains its source, copyright, license, and modification
notice.

## Design references

The following projects informed design decisions, but the project records do
not identify copied implementation in the corresponding hukou components:

- [stew](https://github.com/marwanhawari/stew), evaluated at `8a9a3ea`,
  MIT. Its license is retained at
  [LICENSES/stew-MIT.txt](LICENSES/stew-MIT.txt).
- [ubi](https://github.com/houseabsolute/ubi), evaluated at `edfac51`,
  Apache License 2.0.
- [soar](https://github.com/pkgforge/soar), evaluated at `cc0526e`, MIT.

The detailed source relationship and maintenance rules are recorded in
[docs/VENDORED.md](docs/VENDORED.md). A design reference is
not a claim of authorship over the referenced project.

## Go module dependencies

The shipped binary includes Go modules recorded in `go.mod` and `go.sum`.
The direct runtime dependencies are:

- `github.com/spf13/cobra` v1.8.1 — Apache License 2.0
- `github.com/spf13/pflag` v1.0.5 — BSD 3-Clause License
- `github.com/inconshreveable/mousetrap` v1.1.0 — Apache License 2.0
- `golang.org/x/mod` v0.38.0 — BSD 3-Clause License

Their copyrights and license terms remain with their respective authors and
contributors. The dependency list in this file is informational; `go.mod`
and `go.sum` are the authoritative version records.

The BSD notices are also retained as
[LICENSES/pflag-BSD-3-Clause.txt](LICENSES/pflag-BSD-3-Clause.txt) and
[LICENSES/golang-x-mod-BSD-3-Clause.txt](LICENSES/golang-x-mod-BSD-3-Clause.txt).
The Go additional patent grant is retained as
[LICENSES/golang-x-mod-PATENTS.txt](LICENSES/golang-x-mod-PATENTS.txt).
For convenient review, the pflag notice is reproduced below:

> Copyright (c) 2012 Alex Ogier. All rights reserved.
> Copyright (c) 2012 The Go Authors. All rights reserved.
>
> Redistribution and use in source and binary forms, with or without
> modification, are permitted provided that the following conditions are met:
>
> - Redistributions of source code must retain the above copyright notice,
>   this list of conditions and the following disclaimer.
> - Redistributions in binary form must reproduce the above copyright notice,
>   this list of conditions and the following disclaimer in the documentation
>   and/or other materials provided with the distribution.
> - Neither the name of Google Inc. nor the names of its contributors may be
>   used to endorse or promote products derived from this software without
>   specific prior written permission.
>
> THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS "AS IS"
> AND ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT LIMITED TO, THE
> IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR A PARTICULAR PURPOSE
> ARE DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT OWNER OR CONTRIBUTORS BE
> LIABLE FOR ANY DIRECT, INDIRECT, INCIDENTAL, SPECIAL, EXEMPLARY, OR
> CONSEQUENTIAL DAMAGES (INCLUDING, BUT NOT LIMITED TO, PROCUREMENT OF
> SUBSTITUTE GOODS OR SERVICES; LOSS OF USE, DATA, OR PROFITS; OR BUSINESS
> INTERRUPTION) HOWEVER CAUSED AND ON ANY THEORY OF LIABILITY, WHETHER IN
> CONTRACT, STRICT LIABILITY, OR TORT (INCLUDING NEGLIGENCE OR OTHERWISE)
> ARISING IN ANY WAY OUT OF THE USE OF THIS SOFTWARE, EVEN IF ADVISED OF THE
> POSSIBILITY OF SUCH DAMAGE.

## Distribution requirements

Source and binary distributions of hukou must include:

1. the root [LICENSE](LICENSE);
2. this `THIRD_PARTY_NOTICES.md` file;
3. the applicable files under [LICENSES/](LICENSES/); and
4. any source-level attribution headers on adapted files.

When adding or materially adapting third-party code, update the source file
header, `docs/VENDORED.md`, this notice, and the release packaging in
the same pull request.
