# Contributing to f9s

Thanks for helping make Flink operations less painful.

## Before you start

- Search the issue tracker for related work.
- Open an issue before a large feature or behavior change so the design can be
  agreed before implementation.
- Report suspected vulnerabilities privately through the
  [security policy](SECURITY.md), not in a public issue.

## Development setup

f9s requires Go 1.27 or newer.

```sh
git clone https://github.com/imankhadiv/f9s
cd f9s
go mod download
make test
make lint
make build
```

To exercise the TUI against a disposable local Flink cluster, Docker is also
required:

```sh
make playground
make run
```

Use `make chaos` when you need the built-in failure scenarios.

## Pull requests

- Keep each pull request focused on one problem.
- Add or update tests for behavior changes. Bug fixes should include a
  regression test when practical.
- Run `make test`, `make lint`, and `make build` before submitting.
- Include terminal screenshots or recordings when a visible TUI flow changes.
- Preserve the safety model: mutating actions require confirmation, respect
  `readOnly`, and write to the audit trail.
- Use a concise commit subject such as `feat: ...`, `fix: ...`, or `docs: ...`;
  release notes are grouped from these prefixes.
- Call out compatibility assumptions, especially the Flink version and
  deployment mode tested.

If AI assisted the contribution, say how in the pull request and verify the
result yourself. Maintainers may close low-effort generated changes that lack
tests, evidence, or a clear understanding of the affected code.

By contributing, you agree that your contribution is licensed under the
[Apache License 2.0](LICENSE).
