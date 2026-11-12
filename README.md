# digisafe

[![ci](https://github.com/rhugga/digisafe/actions/workflows/ci.yml/badge.svg)](https://github.com/rhugga/digisafe/actions/workflows/ci.yml)

Keep private records (bank cards, bank accounts, logins, notes) in a local encrypted "safe".

- The safe is an [age](https://age-encryption.org)-encrypted file in `~/.digisafe`
  (`%USERPROFILE%\.digisafe` on Windows). Nothing leaves your machine: no server, no sync, no
  account.
- It unlocks with a passphrase, and also with a YubiKey or (on a Mac) Touch ID once you add one.
- Secrets are never taken as command-line arguments, never echoed as you type them, and never
  shown by `list` or `query`.
- On a terminal, records are added and edited in a form you can move around in before saving.
- Runs on macOS, Linux and Windows.

See [digisafe.md](digisafe.md) for the design: encryption, file formats, locking, and how secrets
are handled.

## Contents

- [Install](#install)
- [Quick start](#quick-start)
- [Record types](#record-types)
- [Adding and editing records](#adding-and-editing-records)
- [Finding and using records](#finding-and-using-records)
- [Several safes and the context](#several-safes-and-the-context) (and [merging](#merging-safes))
- [Keys: passphrase, Touch ID, YubiKey](#keys-passphrase-touch-id-yubikey)
- [Export, import and backups](#export-import-and-backups)
- [Command reference](#command-reference)
- [Files](#files)
- [Windows](#windows)
- [Develop](#develop)

## Install

Download a release from [GitHub releases](https://github.com/rhugga/digisafe/releases): there's
an archive for macOS, Linux and Windows (amd64 and arm64), each holding the `digisafe` binary,
and a `SHA256SUMS` file to check them against:

```shell
sha256sum --check --ignore-missing SHA256SUMS   # macOS: shasum -a 256 --check --ignore-missing SHA256SUMS
tar -xzf digisafe-0.1.0-darwin-arm64.tar.gz
install -m 0755 digisafe-0.1.0-darwin-arm64/digisafe ~/bin/
```

The macOS binaries aren't signed or notarized: if you downloaded the archive with a browser,
macOS blocks it the first time; `xattr -d com.apple.quarantine ~/bin/digisafe` allows it.

Or with Go 1.27 or later:

```shell
go install github.com/rhugga/digisafe@latest
```

Or from a checkout:

```shell
make install   # builds bin/digisafe and installs it to ~/bin (PREFIX=/some/dir to change)
make cross     # every platform: bin/digisafe-darwin-arm64, bin/digisafe-windows-amd64.exe, ...
```

Optional, for unlocking with hardware: [age-plugin-se](https://github.com/remko/age-plugin-se)
(Touch ID) or [age-plugin-yubikey](https://github.com/str4d/age-plugin-yubikey), on `PATH`.

## Quick start

```shell
digisafe safe create                # once: choose a passphrase (12+ characters)
digisafe record add login           # opens a form; fill it in, review, save
digisafe ls                         # id, type, name, description: never secrets
digisafe record show 4f2a9c         # one record in full; any unique id prefix works
digisafe record copy 4f2a9c password  # to the clipboard, cleared after 45s
```

Every command has a short form, so the same thing is:

```shell
digisafe s c
digisafe r a login
digisafe ls
digisafe r s 4f2a9c
digisafe r c 4f2a9c password
```

`digisafe <command> --help` describes any command and its flags.

## Record types

| Type | Fields (`*` = secret) |
| ---- | --------------------- |
| `login` | `url`, `username`, `password*`, `email_address`, `pin*`, `totp_secret*`, `token*` (an API or access token), secret questions (question, `answer*`); keeps the last 5 values of each secret (see [Previous secrets](#previous-secrets-logins)) |
| `bank_card` | credit, debit, prepaid and gift cards: `full_name`, `card_network` (visa, mastercard, amex, ...), `card_no*`, `exp_date` (MMYY), `cvv*`, `pin*`, `zip_code`, `bank_name`, `bank_phone` |
| `bank_account` | `account_no*`, `account_type`, `account_routing_no`, `bank_name`, `bank_phone`, `bank_url`, address (`street_1`, `street_2`, `city`, `state`, `zip_code`), secret questions (question, `answer*`) |
| `note` | `content*` |

Every record also has a `name` (required), `description`, `tags`, `notes` and any number of
custom fields (`key: value`, treated as secrets). `digisafe record types` prints this list.

Some fields are checked:

- `totp_secret` must be a base32 seed or an `otpauth://` URI; anything else is refused.
- `card_no` (Luhn checksum), `exp_date` (MMYY), `cvv` (3 or 4 digits), `pin` (4 to 12 digits)
  and `card_network` (one of visa, mastercard, amex, discover, jcb, unionpay, diners, maestro)
  only warn, since a typo, or a store's own gift card, shouldn't stop you saving. A known network
  is saved in lower case (`VISA` becomes `visa`); in the form, ctrl+e completes one.

`bank_card` was called `credit_card` before it covered debit and gift cards, and a login's
`username` was called `login`. Old safes, exports and files still read: their cards become
`bank_card` records and their logins' `login` becomes `username`. The old names still work:
`record add credit_card`, `--type credit_card`, `--field login=me`, `record copy <id> login`.

## Adding and editing records

### The form

On a terminal, `record add <type>` and `record edit <id>` open a form:

```text
┃ New login
┃ tab / shift+tab move between fields; ctrl+c cancels
┃
┃ name
┃ > GitHub
┃ description
┃ > personal account
┃ tags
┃ comma-separated
┃ > dev, personal
┃ url
┃ > https://github.com
┃ password
┃ > ••••••••
┃ ...
```

- **Moving around**: tab, shift+tab and the arrow keys move between fields, and Enter goes to
  the next one. The first page has the name, description, tags and the type's fields. The second
  has the address (bank accounts) and the notes. In the notes box, alt+enter or ctrl+j starts a
  new line and Enter finishes the page.
- **Secrets** are masked as you type.
- **Checks**: an invalid TOTP secret keeps you in the field until it's fixed. Card number,
  expiry, CVV, PIN and card network problems show a warning under the field and let you carry
  on. The card network field suggests the known networks: ctrl+e completes one.
- **Review**: when the form is done, the record is shown as a card, secrets masked, with:
  - **Save**
  - **Edit the fields**: back to the form, values kept
  - **Secret questions** (logins and bank accounts): add question/answer pairs, or remove them
  - **Custom fields**: add or change a field, or remove one
  - **Show the secrets / Hide the secrets**
  - **Cancel**
- **Cancelling**: ctrl+c at any point, or Cancel at the review, leaves the safe unchanged.
- **Other commands keep working** while a form is open: the safe isn't locked while you type. If
  another digisafe saves it meanwhile, your new record is added to the latest version; an edit is
  saved unless the other save changed or deleted that same record, and then nothing is saved and
  digisafe says why.
- **Editing**: `record edit <id>` opens the same form filled in with the record's current
  values. Without an id, it first shows a list of the records to pick from (↑/↓ move, / filters,
  Enter picks). `--type` narrows the list:

  ```shell
  digisafe record edit                 # pick any record
  digisafe r e --type login            # pick one of the logins
  ```

  The list shows what `record list` shows (id, type, name, description), never a secret. It needs
  the form, so with `--plain` or no terminal the id is required.

The form is drawn on stderr, so stdout stays clean. It's used whenever there's a terminal to
read keys from (stdin, or the terminal itself when stdin is a pipe) and stderr is a terminal.

### Line-by-line prompts

`--plain` on `record add` and `record edit` (or `DIGISAFE_PLAIN=1`, for every command that has
it) asks for each field in turn instead, as it does whenever there
is no terminal for a form:

```shell
digisafe record add login --plain
digisafe record edit 4f2a9c --plain   # Enter keeps a value, "-" clears it
```

### From flags

The non-secret fields can be given as flags. Then only the secret fields are prompted for, and
`--no-prompt` skips those too:

```shell
digisafe record add login --name GitHub --tags dev,work \
    --field url=https://github.com --field username=me       # then asks for password, PIN, TOTP, token
digisafe record add bank_account --name Checking \
    --field bank_name="Credit Union" --field address.city=Springfield --no-prompt
digisafe record add note --name "wifi" --description "home network"   # then asks for the content
```

`--field` takes `name=value` for a type field, or `address.<name>=value`, and can be repeated.
Values may contain commas. Secret fields are refused as flags: command lines end up in shell
history and the process list.

### From JSON

A whole record, secrets included, can come from a file or from stdin:

```shell
some-tool --json | digisafe record add login --file -   # nothing touches the disk
digisafe record add login --file github.json            # digisafe reminds you to delete it
```

```json
{
  "name": "GitHub",
  "description": "personal account",
  "tags": ["dev"],
  "fields": { "url": "https://github.com", "username": "me", "password": "..." },
  "secret_questions": [ { "question": "first pet?", "answer": "..." } ],
  "custom": { "recovery codes": "..." },
  "notes": ""
}
```

Unknown fields are errors. Even with stdin piped in, the passphrase is read from the terminal.

## Finding and using records

```shell
digisafe ls                          # every record (same as `digisafe record list`)
digisafe ls --type login             # only logins
digisafe query github.com            # search names, descriptions, tags, urls, usernames... (digisafe q)
digisafe record show 4f2a9c          # everything, secrets included
digisafe record show 4f2a9c --history  # plus a login's previous passwords, PINs and tokens
digisafe record copy 4f2a9c password # copy one field; cleared after 45s
digisafe record copy 4f2a9c card_no --timeout 10s
digisafe record otp 4f2a9c           # the login's current 2FA code, and how long it's valid
digisafe record otp 4f2a9c --copy    # copied instead, cleared when the code expires
digisafe record del 4f2a9c           # asks first; --force doesn't
```

- **Ids**: records have random UUIDs. Lists show the first 8 characters, and any unique prefix
  works, like git hashes.
- **list and query** print `ID TYPE NAME DESCRIPTION UPDATED`, never a secret. They print which
  safe they read on stderr, so you always know which one you're looking at. `query` searches the
  name, description, tags and every field that isn't secret (`url`, `username`, `bank_name`, the
  address, ...); secrets are never searched.
- **show** draws a card on a terminal, and prints plain `field: value` lines when piped or with
  `--plain`.
- **copy** takes a field name (`password`, `card_no`, ...), or `name`, `description`, `notes`,
  `address.<field>`, `custom.<key>`, `answer.<n>` for secret question n, or
  `history.<field>.<n>` for a previous value (`history.password.1` is the last password). The clipboard is
  cleared after the timeout, unless you've copied something else since. `--timeout 0` never
  clears it.
- **The clipboard stays on this machine.** On macOS the value is copied for this Mac only (not
  handed to your other devices by Universal Clipboard) and marked concealed, so clipboard managers
  that honour `org.nspasteboard.ConcealedType` don't record it. On Windows it's kept out of the
  clipboard history (Win+V) and cloud clipboard sync. On Linux, `wl-copy`, `xclip` and `xsel` have
  no such marker: a clipboard manager may keep it until it's cleared.
- **Text from imported files is shown escaped.** Control characters and the like in a record
  (an escape sequence in a name, say) are printed as `\x1b`, never sent to the terminal.

### Previous secrets (logins)

When you edit a login and change or clear its `password`, `pin`, `totp_secret` or `token`, the
old value isn't thrown away: the login keeps the last 5 values of each, with when they were
replaced. Handy when a site rejects the new password, or a service still uses the old token.

```shell
digisafe record edit 4f2a9c                        # change the password
# kept the previous password as history.password.1 (1 kept; `record show --history`)
digisafe record show 4f2a9c --history
digisafe record copy 4f2a9c history.password.1     # the password before the current one
```

`record show` mentions when a record has previous values. They're stored encrypted in the safe
like everything else, and go with `export` and `import`.

## Several safes and the context

```shell
digisafe safe create work --use      # ~/.digisafe/work.age, and make it the current safe
digisafe ls                          # now lists work.age
digisafe safe list                   # every safe in ~/.digisafe; * marks the current one
digisafe context show                # which safe, and why: --safe, the context, or the default
digisafe context set mysafe          # switch back (a name, or a path)
digisafe context clear               # back to the default, mysafe.age
digisafe --safe work ls              # one command against another safe
```

Which safe a command uses:

1. `--safe` (or `DIGISAFE_SAFE`): a name in the base directory (`work` means `work.age`) or a
   path.
2. Otherwise the context, set by `context set` or `safe create --use`.
3. Otherwise `mysafe.age` in the base directory.

The passphrase prompt names the safe (`passphrase for work.age:`). If the context points at a
safe that has been moved or deleted, commands say so rather than quietly using another one.

**Keys live beside their safe.** Safes in the same directory share one key and passphrase, so a
new safe in `~/.digisafe` opens with the passphrase you already have. A safe somewhere else gets
its own key in its own directory and travels with it:

```shell
digisafe safe create /Volumes/usb/family.age --use   # asks for a new passphrase for the stick
```

`--dir` (or `DIGISAFE_DIR`) changes the base directory, where named safes and the context live.

### Merging safes

```shell
digisafe safe merge work --dry-run   # what would change in the current safe
digisafe safe merge work             # copy work's records into the current safe
digisafe --safe work safe merge mysafe   # and the other way round, to make them the same
```

`safe merge <other>` copies the other safe's records into the current one (`--safe`, else the
context); the other safe isn't changed. Records are matched by id:

- only in the other safe: added;
- in both: the version updated most recently is kept, with its update time. A login keeps the
  other version's password, PIN, TOTP secret and token in its history if they differ, and the two
  histories are combined;
- in both, updated at exactly the same time but different: this safe's version is kept, and the
  merge says so.

It prints what it adds and updates (on stderr), then a summary. Merging the same safe again
changes nothing. A safe in another directory has its own key, so its passphrase is asked too.

It's a union, so two things to know: a record deleted from one safe comes back if the other
still has it; and the same login added separately to each safe has two ids, so it ends up twice
(delete one).

## Keys: passphrase, Touch ID, YubiKey

Each safe directory has an X25519 key pair. The private key is sealed with your passphrase, and
the safe can also be encrypted to hardware keys, each an independent way in. Unlocking tries the
hardware keys first (a Touch ID prompt or a key touch), then falls back to the passphrase.
`--passphrase` skips the hardware keys.

```shell
digisafe keys list                   # the passphrase key and any hardware keys
digisafe keys passwd                 # change the passphrase (12+ characters)
digisafe keys add <identity-file>    # add a hardware key
digisafe keys remove <n>             # remove hardware key n (see keys list); needs the passphrase
digisafe keys rekey                  # new key pair: if the key file or passphrase may be compromised
```

The keys belong to the directory, so every command here applies to every safe in it:

- `keys add` and `keys remove` re-encrypt all the safes in the directory and their backups, so an
  added key opens all of them, and a removed one (a lost YubiKey, say) opens no version of any.
  Both need the passphrase.
- `keys rekey` makes a new key pair and re-encrypts every safe in the directory and its backups
  to it.
- `keys passwd` changes the passphrase for all of them (only the key file is re-sealed).

Other `.age` files in the directory that aren't encrypted to its key are left alone, with a note.

### Touch ID (macOS)

```shell
brew install age-plugin-se
age-plugin-se keygen --access-control=any-biometry -o ~/.digisafe/se-key.txt
digisafe keys add ~/.digisafe/se-key.txt
```

1. **Install the plugin.** [age-plugin-se](https://github.com/remko/age-plugin-se) makes and uses
   keys in the Mac's Secure Enclave. It has to be on `PATH` whenever digisafe unlocks.
2. **Make a key.** `keygen` creates a key inside the Secure Enclave; the private key never
   leaves it. `se-key.txt` only holds a reference to it, so it isn't secret. `--access-control`
   says what unlocking takes:

   | Value | Unlocks with |
   | ----- | ------------ |
   | `any-biometry` | any enrolled fingerprint |
   | `any-biometry-or-passcode` | a fingerprint, or your Mac login password when Touch ID isn't available (lid closed) |
   | `current-biometry` | only the fingerprints enrolled now: adding or removing one later makes the key unusable |

3. **Add it.** `keys add` asks for your passphrase, then re-encrypts every safe in the directory
   (and their backups) so Touch ID opens all of them. The key's reference is copied into
   `plugin-keys.txt`, so `se-key.txt` can be deleted afterwards.

Check it worked:

```shell
digisafe keys list    # the passphrase key, and an "se" key
digisafe ls           # asks for Touch ID, not the passphrase
```

- The key exists only on this Mac. On another Mac (a synced safe, say) digisafe notes that the
  hardware key didn't unlock the safe and asks for the passphrase.
- `--passphrase` skips Touch ID for one command.
- A safe in another directory has its own keys: add Touch ID to it with
  `digisafe --safe /path/to/it.age keys add se-key.txt`.
- To stop using it: `digisafe keys remove <n>`, with the number from `keys list`. Every safe in
  the directory is re-encrypted without it.

### YubiKey (macOS, Linux, Windows)

```shell
# install age-plugin-yubikey (https://github.com/str4d/age-plugin-yubikey) so it's on PATH
age-plugin-yubikey --generate > ~/.digisafe/yubikey-key.txt
digisafe keys add ~/.digisafe/yubikey-key.txt
```

1. **Install the plugin.** [age-plugin-yubikey](https://github.com/str4d/age-plugin-yubikey) is on
   Homebrew (`brew install age-plugin-yubikey`), and has release binaries for Linux and Windows.
   On Linux it needs the PC/SC daemon (`pcscd`) running; on Windows, the Smart Card service.
2. **Make a key.** `--generate` creates a key in the YubiKey's PIV applet; it never leaves the
   YubiKey. The file only references it, so it isn't secret. Useful options:
   - `--touch-policy always|cached|never`: whether each unlock needs a touch (default `always`;
     `cached` accepts one touch for 15 seconds).
   - `--pin-policy once|always|never`: when the PIV PIN is asked (default `once` per session).
   - `--slot <n>` to use a slot other than the next free one, if the YubiKey's PIV is in use.

   The first time, it asks you to change the YubiKey's default PIN and PUK if they're still set.
3. **Add it.** As with Touch ID: `keys add` asks for the passphrase and re-encrypts every safe in
   the directory. Unlocking then asks for the PIN and a touch.

Unlike Touch ID, the key travels with the YubiKey: any computer with the plugin installed and a
copy of the safe directory (which holds `plugin-keys.txt`) unlocks with it.

- **Add a second YubiKey** the same way, as a spare: each key is an independent way in.
- **Lost one?** `digisafe keys remove <n>` with the passphrase: no version of any safe in the
  directory opens with it after that.

The passphrase always keeps working, so store it somewhere safe. Without it or a hardware key,
the safe can't be opened. There is no recovery.

## Export, import and backups

```shell
digisafe safe export --out records.json    # every record as plaintext JSON (asks first; mode 0600)
digisafe safe export | other-tool          # to stdout
digisafe safe import records.json          # one record, a list, or an export; - reads stdin
digisafe safe import records.json --replace  # replace records whose id already exists
```

`export` refuses to overwrite a file without `--force` and asks before writing unless `--yes`.
`import` skips records already in the safe (by id) unless `--replace`, which keeps the replaced
login's secrets in its history, as an edit would. A file with the same id twice is refused. Both
remind you that the plaintext file still holds your secrets.

Every save keeps the previous version: `mysafe.age.1` (newest) to `mysafe.age.5`, encrypted
like the safe. To roll back, copy one over `mysafe.age`.

## Command reference

```text
digisafe safe (s)
  create (c) [name|path] [--use]    create a safe (default mysafe); --use makes it the context
  list (l, ls)                      the safes in --dir, * marking the current one
  merge (m) <other> [--dry-run]     copy another safe's records in; the newest version of each wins
  export [--out f] [--force] [--yes]  every record as plaintext JSON
  import <file|-> [--replace]       add records from JSON
digisafe record (rec, r)
  add (a) <type> [--plain]          the form (or prompts); see "From flags" and "From JSON"
  list (l, ls) [--type t]           id, type, name, description: never secrets
  show (s) <id> [--history] [--plain]  one record in full, secrets included (and previous secrets)
  edit (e) [id] [--type t] [--plain]  the form, filled in; no id: pick from a list (of type t)
  del (d, rm) <id> [--force]        delete (asks first)
  copy (c) <id> <field> [--timeout 45s]  copy a field to the clipboard
  otp (o) <id> [--copy]             the login's current TOTP code
  types                             the record types and their fields
digisafe keys (key, k)
  list | add <identity-file> | remove <n> | passwd | rekey
digisafe query (q) <term> [--type t]  search name, description, tags and non-secret fields
digisafe context (ctx)
  show | set <name|path> | clear
digisafe ls                         shortcut for `record list`
```

Global flags (before or after the command):

| Flag | Env | Meaning |
| ---- | --- | ------- |
| `--dir` | `DIGISAFE_DIR` | base directory (default `~/.digisafe`) |
| `--safe` | `DIGISAFE_SAFE` | the safe: a name in `--dir` or a path |
| `--passphrase` | | unlock with the passphrase, not a hardware key |

`record add`, `record edit` and `record show` also take `--plain` (env `DIGISAFE_PLAIN`):
line-by-line prompts and plain output, no form or colours. The other commands always print plain
text. Colours are also turned off by `NO_COLOR`, and whenever output isn't a terminal.

## Files

In `~/.digisafe` (created mode 0700), or beside a safe kept elsewhere:

| File | What | Secret? |
| ---- | ---- | ------- |
| `mysafe.age`, `work.age`, ... | the safes, age-encrypted | encrypted |
| `mysafe.age.1` .. `.5` | each safe's last 5 versions | encrypted |
| `identity.age` | the key pair's private key, sealed with the passphrase | encrypted |
| `identity.pub` | its public key | no |
| `plugin-keys.txt` | handles to the hardware keys (the keys stay in the hardware) | no |
| `context` | the path of the current safe | no |
| `.lock` | lock file, so two digisafes don't write at once | no |

Files are mode 0600. Back up the whole directory: a safe without its `identity.age` (or a
hardware key) can't be opened. These names are taken: a safe can't be called `identity`,
`plugin-keys.txt`, `.lock`, `context` or `x.age.1` (a backup's name), or start with a dot.

digisafe creates directories mode 0700 but doesn't change ones that exist. Create a safe in a
folder others can list (`~/Documents` on Linux, a shared drive) and it says so; the files are
encrypted and 0600 either way. A safe on read-only media (a write-protected USB stick) can be
read, but not changed.

## Windows

The passphrase and YubiKeys work. Touch ID is Mac only. Use the Windows console or Windows
Terminal. In Git Bash, run digisafe through `winpty`, since mintty hides the terminal that the
secret prompts and the form need. See [digisafe.md](digisafe.md#windows) for how locking, file
permissions and the clipboard work there.

## Develop

```shell
make all       # check, then build bin/digisafe and every platform's binary
make check     # gofmt, vet (macOS, Linux and Windows), and the tests with the race detector
make test      # just the tests
make coverage  # test coverage across all packages: functions under 80%, and the total
make coverage HTML=1   # the same, then a line-by-line report in the browser
make build     # bin/digisafe
make cross     # every platform into bin/
make help      # every target
```

The version lives in `version.sh` (`./version.sh` prints it). `make bump-version` sets a new
one there and in its copies, the `Version` constant in `internal/app/app.go` and the file names
in this README, and checks none is left behind; a test fails if they ever disagree.

```shell
make version                     # 0.1.0 (v0.1.0)
make bump-version                # 0.1.0 -> 0.1.1 (TO=patch is the default)
make bump-version TO=minor       # 0.1.1 -> 0.2.0; TO=major -> 1.0.0
make bump-version TO=1.0.0-rc.1  # an exact version; TO=patch then releases it as 1.0.0
```

Releasing, from a clean, pushed `main`:

```shell
# make bump-version, then commit and merge that to main, then
make release-check   # gh logged in, clean tree, main pushed, the tag v<version> unused
make dist            # make check, then the archives and SHA256SUMS in dist/ (to look at first)
make release         # all of that, then tag v<version>, push the tag, publish the GitHub release
make release NOTES=notes.md   # with your own release notes (default: generated from the commits)
```

`make release` builds and checks everything before it tags, so a failure leaves no tag behind.
If it stops after pushing the tag (`gh` not logged in, say), `make release-publish` creates the
GitHub release from that tag and `dist/`. `gh` must be logged in to github.com itself
(`gh auth status -h github.com`); `GH_HOST=...` releases somewhere else.

Layout:

```text
version.sh              the version (source of truth); scripts/bump-version.sh changes it
main.go                 entry point
internal/app            the CLI (urfave/cli v3) and the commands
internal/tui            the record form (huh / bubbletea) and styled output (lipgloss)
internal/record         record types, schemas, validation, search
internal/safe           the safe document, encryption and keys, storage (atomic writes, backups, lock)
internal/prompt         line-by-line terminal prompts
internal/clipboard      clipboard copy and delayed clear
internal/totp           RFC 6238 TOTP
```

The tests run without a terminal. The form is driven by sending key presses to it directly, and
hardware keys are faked with a test age plugin. CI (`.github/workflows/ci.yml`) checks gofmt
and vet for every platform, and runs the tests on Linux, macOS and Windows.
