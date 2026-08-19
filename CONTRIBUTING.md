# Contributing

Thanks for your interest. This document covers the license, the contributor agreement, and how to
get a change merged.

## License

This project is licensed under the **GNU Affero General Public License v3.0** (AGPL-3.0). The full
text is in [LICENSE](LICENSE).

### What that means for you as a user

Two obligations exist under AGPL, and neither applies to ordinary use:

| What you do | What you owe |
|---|---|
| Run it on your own machine, modified or not | Nothing |
| Use it inside your company, each developer running it locally | Nothing |
| Modify it and let other people use it over a network | Offer those users the source of your modified version |
| Distribute a modified binary | Ship the corresponding source |
| Host it as a service for others | Publish your modifications |

**Using this tool does not place your code under AGPL.** It runs as its own process and agents talk
to it over MCP, which is inter-process communication, not linking. Your application, your agent
harness, and anything else on your machine remain under whatever license they already had.

## Contributor License Agreement

Before your first pull request is merged, you need to sign the CLA.

### Why

You keep the copyright to everything you write. The CLA is a grant of permission, not a transfer of
ownership. It gives the maintainer the right to distribute your contribution under licenses other
than AGPL-3.0.

That matters because a hosted version of this project is planned. Without the grant, contributed
code could only ever ship under AGPL, which would split the codebase into parts that can be offered
as a service and parts that cannot. The split gets worse over time and is impossible to unwind once
contributors become unreachable.

Asking for it up front is the only workable moment. Retrofitting a CLA means chasing every past
contributor, and code from anyone who cannot be reached has to be removed.

### What you grant

- You keep your copyright.
- You grant the maintainer a perpetual, worldwide, non-exclusive, royalty-free license to use,
  reproduce, modify, and distribute your contribution, **including under licenses other than
  AGPL-3.0**.
- You confirm you have the right to make the contribution — that it is your own work, or that you
  have permission from whoever owns it (your employer, typically).
- You provide the contribution as-is, with no warranty.

Your contribution also remains available under AGPL-3.0 to everyone, exactly like the rest of the
project. Nothing you grant here removes it from the open source release.

### How to sign

Comment on your pull request with:

```
I have read the CONTRIBUTING.md and I accept the Contributor License Agreement.
```

One signature covers all your future contributions. If you are contributing on behalf of an
employer, make sure someone authorized to bind the company signs instead.

## Making a change

### Before you write code

Open an issue first for anything beyond a bug fix or a typo. Design decisions in this project follow
the spec in `doc/specs/`, and a change that contradicts the spec needs the spec updated first.
Finding that out after you have written the code wastes your time.

### Development

Requirements:

- Go 1.26 or later
- Node 22.12 or later and pnpm, **only if you are changing the web UI**

Frontend assets are generated during the build and are not committed. On a fresh checkout, generate
them once before building the Go binary locally:

```sh
cd web
pnpm install
pnpm build      # writes web/dist/, which is embedded into the binary
```

If you skip this step, the binary embeds only the `.gitkeep` sentinel and the web UI is blank.
GoReleaser runs the equivalent frozen-lockfile install and frontend build automatically.

```sh
go build ./...
go test ./... -race
go vet ./...
```

After changing the web UI, rebuild the generated assets:

```sh
cd web
pnpm build      # writes web/dist/, which is embedded into the binary
```

### Tests

Changes need tests. This project is built test-first, and the implementation plan in `doc/plans/`
shows the expected shape: write the failing test, watch it fail, write the minimal code, watch it
pass.

Two rules are absolute:

- Do not weaken or delete a test to make a build pass. Fix the implementation instead.
- Do not add a test that passes against a broken implementation. Confirm it fails first.

### Commits and pull requests

- Write commit messages in English.
- Use [Conventional Commits](https://www.conventionalcommits.org/) prefixes: `feat:`, `fix:`,
  `docs:`, `test:`, `refactor:`, `chore:`.
- Keep a pull request to one concern. Several unrelated fixes are several pull requests.
- Explain what breaks without the change, not just what the change does.

## Code of conduct

Be straightforward and stay on the technical substance. Disagreement is fine and useful; contempt is
not. Maintainers will close threads that stop being about the work.
