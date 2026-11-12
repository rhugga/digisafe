# Release notes

## Unreleased

### Added

- Record names are unique in a safe, whatever the type, ignoring case and extra spaces. A taken
  name is caught as you type it (in the form or at the prompt), and with `--name` before any
  secret is asked for.
- `record show` takes a record's name as well as its id: `digisafe record show "my bank"`.
- `bank_account` has a `secret_pin`: the PIN your bank asks for on the phone (secret; digits, at
  most 12, with a warning otherwise).
- `make bump-version TO=patch|minor|major|X.Y.Z` sets the version everywhere it's copied, from
  `version.sh`, the new source of truth; a test fails if the copies disagree.
- `make release-publish` creates the GitHub release from an already pushed tag, for a release
  that stopped after tagging; `make all`, `make coverage` and `make release-notes`.
- `.github/CODEOWNERS`.

### Changed

- `safe merge` skips (and lists) a record whose name another record already has, rather than
  adding a second one; `safe import` refuses a file that would duplicate a name.
- `make release` checks that `gh` is logged in to github.com itself, not just to some host, and
  publishes this file's section for the version as the release notes.

### Upgrading

- A safe that already has two records with the same name still opens. Looking up that name
  lists their ids; editing one of them asks you to rename it.

## 0.1.0 (2026-11-11)

The first mvp release.

- **Safes**: age-encrypted files in `~/.digisafe`, unlocked with a passphrase (scrypt) and
  optionally Touch ID (Secure Enclave) or a YubiKey. Several safes, a current-safe context,
  `safe merge`, export and import, and the last 5 versions of each safe kept as backups.
- **Records**: logins (with TOTP codes, secret questions, and the last 5 values of each secret),
  bank cards, bank accounts and notes, plus custom fields. Secrets are never taken as arguments,
  echoed, searched or listed.
- **Terminal UI**: a form to add and edit records, a list to pick one from, and styled output;
  `--plain` for line-by-line prompts.
- **Clipboard**: `record copy` clears it after a timeout; on macOS and Windows copies are kept
  out of clipboard history and sync.
- macOS, Linux and Windows, amd64 and arm64.
