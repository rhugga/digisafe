# digisafe design

A command-line tool that keeps private and personal records (cards, bank accounts, logins, notes)
in a local encrypted file, called a **safe**.

## Goals

- Everything on disk is encrypted; secrets are only ever decrypted in memory.
- Unlock with a passphrase, or with a hardware-backed key (the Mac's Secure Enclave with Touch ID,
  or a YubiKey), whichever is configured. Either works on its own.
- No hand-rolled cryptography.
- Secrets never appear in command-line arguments, shell history, listings or search results.
- A crash or a second concurrent run never corrupts the safe.

Platforms: macOS, Linux and Windows. The passphrase and YubiKeys work on all three; the Secure
Enclave (Touch ID) is macOS only. A safe written on one platform opens on the others.

## Encryption

The safe is encrypted with [age](https://age-encryption.org) (`filippo.io/age`): authenticated
encryption (ChaCha20-Poly1305) with a well-specified, widely reviewed file format.

age's passphrase mode (scrypt) must be the *only* recipient of a file, so it can't sit alongside a
hardware key on the same safe. Instead:

- `create` generates an age **X25519 key pair** for the safe. Its private key is stored in
  `identity.age`, itself age-encrypted with your **passphrase** (scrypt). Its public key is in
  `identity.pub`.
- The safe is encrypted to that public key, plus every **plugin key** you add with `keys add`:
  `age-plugin-se` (Secure Enclave, Touch ID) or `age-plugin-yubikey`. Each is an independent way in.
- To unlock, digisafe tries the plugin keys first (a Touch ID prompt or a key touch), then falls
  back to the passphrase. `--passphrase` skips the plugin keys.
- The keys belong to the **directory**: every safe in it is encrypted to the same key pair and
  plugin keys. So `keys add`, `keys remove` and `keys rekey` re-encrypt every safe in the
  directory and every backup (`Safe.ReencryptKeyed`, `Safe.Rekey`), with the private key (they
  need the passphrase). A removed key then opens no version of any safe there. `.age` files the
  key can't open aren't digisafe's: they're skipped, with a note.

Post-quantum (hybrid ML-KEM) age keys are not used: age refuses to mix them with plugin recipients.

### Files (`$HOME/.digisafe`, created mode 0700; `%USERPROFILE%\.digisafe` on Windows)

| File | Contents | Secret? |
| ---- | -------- | ------- |
| `mysafe.age` | the safe: all records, age-encrypted | encrypted |
| `mysafe.age.1` .. `.5` | the last 5 versions of the safe | encrypted |
| `identity.age` | the safe's X25519 private key, passphrase-encrypted | encrypted |
| `identity.pub` | its public key (`age1...`) | no |
| `plugin-keys.txt` | plugin identities (`AGE-PLUGIN-...`), handles to keys held by the hardware | no |
| `.lock` | lock file | no |
| `context` | the path of the current safe (see Contexts) | no |

All files are mode 0600 (on Windows, see "Windows" below). `--dir` (or `DIGISAFE_DIR`) moves the whole set; `--safe` picks another
safe, in it or elsewhere (its keys are beside it).

### Safe format (inside the encryption)

```json
{
  "version": 2,
  "records": [ { ...record... } ]
}
```

`version` lets later releases migrate the format. Version 2 added records' `history`; version 1
safes are read and saved as version 2. A digisafe that only knows version 1 refuses a version 2
safe ("upgrade it") instead of dropping the history it doesn't understand.

### Writing

Every write: take an exclusive lock on `.lock` (reads take a shared lock), encrypt to a temporary
file in the same directory, fsync it, keep the current safe as `mysafe.age.1` (older versions shift
down, `.5` is dropped), rename the new file over `mysafe.age`, fsync the directory. A crash leaves
either the old or the new safe, never a partial one.

`record add` and `record edit` don't hold the lock while you type: they read the safe under a
shared lock, release it (`Safe.Release`) for the form or prompts, then take the exclusive lock
(`Safe.Reacquire`) and compare the file with what they read (sha256). If another digisafe saved
it meanwhile, the safe is read again: a new record is added to that version, and an edited one
replaces its copy there only if the other save left it as it was (else nothing is saved).

Where the lock file can't be created or written (read-only media, a directory you can't write),
reads lock an existing `.lock` through a read-only handle, or with none go ahead unlocked; writes
fail as they must.

Directories are created 0700; existing ones are left alone (`EnsureDir`), since a safe may be
created in a folder the user already has. `safe create` notes when other users can list it.

Safe names can't collide with digisafe's own files (`CheckSafeName`): `identity.age`,
`identity.pub`, `plugin-keys.txt`, `.lock`, `context`, a backup's name (`x.age.N`), or anything
starting with a dot (temporary files).

## Records

Every record has the common fields:

| Field | Notes |
| ----- | ----- |
| `id` | UUID v4 (random). The CLI shows the first 8 characters and accepts any unique prefix, like git hashes. Not v7: its leading timestamp makes ids created close together share a long prefix |
| `type` | `bank_card`, `bank_account`, `login` or `note` (`credit_card`, the old name for `bank_card`, is read as it) |
| `name` | required; unique in the safe, ignoring case and extra spaces (`record.NameKey`); searchable |
| `description` | searchable |
| `tags` | list; searchable |
| `notes` | free text |
| `created_at`, `updated_at` | set automatically |
| `custom` | extra `key: value` fields the schema doesn't cover |

Type-specific fields (`*` = secret: never echoed when typed, shown only by `show`):

- **bank_card** (credit, debit, prepaid and gift cards): `full_name`, `card_network` (the card
  network: visa, mastercard, amex, discover, jcb, unionpay, diners, maestro; others allowed),
  `card_no*`, `exp_date` (`MMYY`), `cvv*` (3 or 4 digits; the security code on the back, on the
  front for Amex), `pin*` (4 to 12 digits), `zip_code`, `bank_name`, `bank_phone`. `card_no` gets
  a Luhn check; `exp_date`, `cvv`, `pin` and `card_network` a format check: warnings, not errors.
  A known network is saved in lower case.
- **bank_account**: `account_no*`, `secret_pin*` (the PIN for telephone banking: digits, at most 12;
  a warning otherwise), `account_type` (e.g. checking, savings), `account_routing_no`,
  `bank_name`, `bank_phone`, `bank_url`, an `address` (`street_1`, `street_2`, `city`, `state`,
  `zip_code`), and `secret_questions`.
- **login**: `url`, `username`, `password*`, `email_address`, `pin*`, `totp_secret*` (a base32 TOTP
  seed or an `otpauth://` URI; validated, used by `otp`), `token*` (an API or access token), and
  `secret_questions`.
- **note**: `content*`, free-form secret text.

`secret_questions` is a list of `{question, answer*}` pairs.

**Secret history (logins).** When `record edit` changes or clears a login's secret field
(`password`, `pin`, `totp_secret`, `token`), the old value is kept in the record's `history`:
newest first, with the time it was replaced, at most 5 per field (`HistoryLimit`). Setting a field
for the first time, or leaving it unchanged, keeps nothing. The history is inside the encrypted
safe like the rest of the record, goes with `export` and `import`, and is shown by
`record show --history` and copied with `record copy <id> history.<field>.<n>` (1 = the most
recent). Other types keep none (`Schema.History`).

**Renamed types and fields.** `bank_card` was `credit_card` until it covered debit and gift
cards; a login's `username` was `login` (the same name as its type). Old names are mapped to new
ones wherever a record is read (the safe, `add --file`, `import`: `Record.Upgrade`) and wherever
a name is typed (`--type`, `record add <type>`, `--field`, `record copy`), so an old safe opens
unchanged and is saved with the new names the next time it's written. The maps are
`renamedTypes` and `renamedFields` in `internal/record/schema.go`.

Record JSON (the shape for `add --file`, `import` and `export`):

```json
{
  "id": "0193f1c2-...",
  "type": "login",
  "name": "example.com",
  "description": "personal account",
  "tags": ["personal"],
  "notes": "",
  "fields": { "url": "https://example.com", "username": "me", "password": "..." },
  "secret_questions": [ { "question": "first pet?", "answer": "..." } ],
  "custom": { "account number": "1234" },
  "history": { "password": [ { "value": "...", "replaced_at": "2026-10-01T09:30:00Z" } ] },
  "created_at": "2026-11-11T12:00:00Z",
  "updated_at": "2026-11-11T12:00:00Z"
}
```

`address` (bank accounts) is an object with the five address fields. Unknown fields are errors.

## Commands

```text
digisafe safe (s)
  create (c) [name|path] [--use]    create a safe (default mysafe); --use makes it the context
  list (l, ls)                      list the safes in --dir, * marking the current one
  merge (m) <other> [--dry-run]     copy another safe's records into this one (newest version wins)
  export [--out f] [--force] [--yes]  write the records as plaintext JSON (asks first)
  import <file|-> [--replace]       add records from a JSON file (a record, a list, or an export)
digisafe record (rec, r)
  add (a) <type> [--plain]          add a record: the form on a terminal, else (or with --plain)
                                    a prompt per field (blank = empty)
  add <type> --name n [--description d] [--tags a,b] [--notes n] [--field k=v ...] [--no-prompt]
                                    the same from flags (non-secret fields only); prompts for just
                                    the secret fields, unless --no-prompt
  add <type> --file f.json          read the record from a JSON file (- reads stdin)
  list (l, ls) [--type t]           list records: id, type, name, description (never secrets)
  show (s) <id|name> [--history] [--plain]  show one record in full, secrets included (and previous secrets)
  edit (e) [id] [--type t] [--plain]  edit a record in the form; no id: pick it from a list (of type t)
  del (d, rm) <id> [--force]        delete a record (asks first unless --force)
  copy (c) <id> <field>             copy a field to the clipboard, cleared after 45s (--timeout)
  otp (o) <id> [--copy]             the login's current TOTP code
  types                             list the record types and their fields
digisafe keys (key, k)
  list                              list the keys that can unlock the safe
  add <identity-file>               add a Secure Enclave / YubiKey key (age plugin identity file);
                                    re-encrypts every safe in the directory
  remove <n>                        remove plugin key n; re-encrypts every safe in the directory
  passwd                            change the passphrase
  rekey                             replace the key pair (re-encrypts every safe in the directory)
digisafe query (q) <term> [--type t]  search name, description, tags and non-secret fields
digisafe context (ctx)
  show                              the current safe and where that choice came from
  set <name|path>                   make a safe the current one (it must exist)
  clear                             go back to the default, mysafe.age
digisafe ls                         shortcut for `record list`
```

Global flags: `--dir`, `--safe`, `--passphrase` (skip plugin keys). `record add`, `record edit`
and `record show` also take `--plain` (`DIGISAFE_PLAIN`: line-by-line prompts and plain output),
the only commands with a form or styled output.

### Terminal UI

When the prompts have a terminal (stdin, or `/dev/tty` / `CONIN$` when stdin is a pipe) and
stderr is one too, `record add` and `record edit` use a form (huh, on bubbletea) drawn on
stderr:

- page 1: name (required), description, tags and the type's fields; page 2: the address (bank
  accounts) and the notes. tab / shift+tab / the arrows move between fields and pages.
- Secret fields are masked inputs. Strict checks (TOTP secret) keep you in the field until the
  value is valid; loose ones (card number, expiry, CVV, PIN, card network) show a warning under
  the field. A field with usual values (card network) completes them with ctrl+e.
- Then a review: the record as a card, secrets masked, and Save / Edit the fields / Secret
  questions / Custom fields / Show the secrets / Cancel. Secret questions and custom fields are
  added and removed from their own menus.
- ctrl+c or Cancel leaves the safe unchanged. `edit` opens the form filled in.
- `edit` without an id first lists the records (id, type, name, description: no secrets) in a
  filterable select; `--type` narrows it. With no form (`--plain`, no terminal) the id is
  required.
- The notes box can't open `$EDITOR` (huh's ctrl+e), which would write them to a temporary file.

`record show` draws the record as a lipgloss card when stdout is a terminal. Colours are
dropped for terminals without them and when `NO_COLOR` is set. Field flags, `--file`, pipes,
redirected output and `--plain` keep the line-by-line prompts and plain text, so scripts see no
change.

### Contexts

The context is the current safe: `<dir>/context` holds its absolute path, nothing else (it's not
secret). Which safe a command uses:

1. `--safe` (or `DIGISAFE_SAFE`), a name in `--dir` (`work` is `work.age`) or a path;
2. else the context;
3. else `<dir>/mysafe.age`.

`context show` says which of these applied. If the context points at a safe that has since been
moved or deleted, commands say so rather than quietly using the default; `context clear` or
`context set` fixes it. `record list` and `query` print the safe they read first, so it's always
clear which one you're looking at, and the passphrase prompt names it.

A safe's keys (`identity.age`, `identity.pub`, `plugin-keys.txt`) and lock are always in the
safe's own directory. Safes in the same directory share one key; a safe on a USB stick carries its
own key with it.

### Merging

`safe merge <other>` (`Document.Merge`) copies the other safe's records into the current one,
matched by id. A record only there is added. For one in both, the version with the later
`updated_at` replaces ours (keeping its own `updated_at`, so merging is the same whichever safe
does it); on a tie with different contents ours stays and it's reported as a conflict. Records
that differ only in their history are the same record. For logins, the losing version's secrets
that differ go into the winner's history (replaced at the winner's `updated_at`), the two
histories are combined (newest first, at most 5 per field), and nothing already there is added
twice, so a repeated merge changes nothing and saves nothing.

The other safe is read under its own lock with its own key, or, in the same directory, with the
current safe's keys and lock (`Safe.ReadSibling`). It is never written. There are no
tombstones: a record deleted in one safe returns from the other. An incoming record that would
take a name another record here has (a different id) isn't merged: it's reported as skipped
(`MergeResult.NameClashes`).

### Names and lookups

Names are unique in a safe by `record.NameKey` (lower case, runs of spaces collapsed).
`Document.Add` refuses a taken name, so every way in checks it: the form and the prompts as
it's typed, `--name` before the secret prompts, `edit` before saving (also against a save by
another digisafe meanwhile), `import` (the whole file or nothing) and `merge` (skipped). A safe
written before names were unique still opens; looking up a duplicated name lists the ids, and
editing one of them asks for a new name.

`record show` finds a record with `Document.Lookup`: a full id first, then the name, then a
unique id prefix. A ref that is one record's name and the start of another's id is an error
asking for more of the id.

## Handling secrets

- Secret fields and passphrases are read from the terminal without echo; there are no flags or
  environment variables for them. `add`'s field flags (`--field name=value`, `address.<name>=value`)
  take only non-secret fields and refuse secret ones, since command lines are kept in shell history
  and shown in the process list. A whole record, secrets included, can come from `--file -` (stdin).
- Prompts read the terminal itself (`/dev/tty`, `CONIN$` on Windows) when stdin is a pipe, so
  `some-tool | digisafe record add login --file -` still asks for the passphrase interactively; with no
  terminal at all, secret prompts refuse rather than read the pipe.
- `record list` and `query` print only id, type, name and description. `query` matches the name,
  description, tags and the fields that aren't secret (url, username, the address...), never a
  secret, so it can't be used to probe one.
- Record text is printed through `record.Printable`: control characters and Unicode
  bidirectional overrides (from an imported file, say) are shown escaped (`\x1b`, `\u202e`), never
  sent to the terminal. Names, descriptions and tags have them replaced by spaces when saved.
- An interrupt (ctrl+c at a passphrase prompt) restores the terminal before exiting with 130,
  so echo isn't left off.
- The form masks secrets as they're typed, and its review masks them unless you choose to show
  them (then, like `record show`, they're in the terminal's scrollback).
- `record copy` hands the value to the clipboard and starts a background process that clears it
  after the timeout, unless the clipboard has changed since:
  - macOS: through NSPasteboard (an `osascript` JavaScript snippet; the value goes on its stdin),
    for this Mac only (`NSPasteboardContentsCurrentHostOnly`: not shared by Universal Clipboard)
    and marked `org.nspasteboard.ConcealedType`, which clipboard managers honour. `pbcopy` is the
    fallback.
  - Windows: the clipboard API, with `ExcludeClipboardContentFromMonitorProcessing`,
    `CanIncludeInClipboardHistory` = 0 and `CanUploadToCloudClipboard` = 0, so it stays out of the
    clipboard history and cloud sync. A plain copy is the fallback.
  - Linux: `wl-copy`, `xclip` or `xsel`, with no such marker. Their output isn't piped: they leave
    a child serving the clipboard, and waiting on it would hang.
- `safe import` refuses a file with the same id twice; `--replace` keeps the replaced login's
  secrets in its history. Ids are stored in canonical form (lower case, hyphens).
- `record add --file` and `safe import` read plaintext files: digisafe warns that the file still holds the
  secrets and should be deleted (not for `-`: piped input never touches the disk). `safe export` asks before writing plaintext, refuses to overwrite a file
  without `--force`, and writes it 0600.
- Passphrases must be at least 12 characters.

## Adding a Touch ID key or a YubiKey

Step by step, with the options: README.md, [Touch ID](README.md#touch-id-macos) and
[YubiKey](README.md#yubikey-macos-linux-windows). In short:

```shell
age-plugin-se keygen --access-control=any-biometry -o se-key.txt   # Touch ID (macOS)
age-plugin-yubikey --generate > yubikey-key.txt                     # a YubiKey
digisafe keys add se-key.txt                                       # asks for the passphrase
```

The identity file holds a reference to the key (`AGE-PLUGIN-SE-...`, `AGE-PLUGIN-YUBIKEY-...`),
not the key, which stays in the Secure Enclave or the YubiKey. `keys add` copies it into
`plugin-keys.txt` and re-encrypts every safe in the directory to it (see Encryption). A Secure
Enclave key works only on the Mac that made it; a YubiKey works wherever its plugin is installed.

## Windows

- **Unlocking:** the passphrase, or a YubiKey. There is no Secure Enclave, and no maintained age
  plugin for Windows Hello or the TPM; an opt-in unlock cache using DPAPI is a possible later
  addition.
- **Files** are in `%USERPROFILE%\.digisafe`. Windows has no Unix file modes: the files are
  protected by the profile directory's ACL (you, Administrators and SYSTEM by default).
- **Locking** uses `LockFileEx` in place of `flock`. Saving still writes a temporary file, fsyncs
  it and renames it over the safe; Windows can't fsync the directory afterwards.
- **Clipboard:** `copy` uses the Windows clipboard API, marked to stay out of the clipboard history
  and cloud sync, and the background process that clears it is started detached (no console, its
  own process group).
- **Typing secrets** works in the Windows console and Windows Terminal. Git Bash's mintty is not
  seen as a terminal: run digisafe through `winpty`, or from another terminal.
- `--safe` accepts a path with either separator (`C:\safes\work.age` or `C:/safes/work.age`).

## Layout

```text
main.go                 entry point
internal/app            the CLI (urfave/cli v3) and the commands
internal/record         record types, schemas, validation, search
internal/safe           the safe document, encryption/keys, storage (atomic writes, backups, lock;
                        lock_unix.go / lock_windows.go)
internal/totp           RFC 6238 TOTP
internal/clipboard      clipboard copy and delayed clear (clipboard_unix.go / clipboard_windows.go)
internal/prompt         terminal prompts
internal/tui            the record form (huh / bubbletea) and styled output (lipgloss)
```

## Original requirements

The starting brief, for reference (changes from it are noted):

- store secret data in an encrypted blob called a "safe" -> an age-encrypted JSON document
- default to `$HOME/.digisafe/mysafe.json` -> `$HOME/.digisafe/mysafe.age`
- use urfave/cli v3
- record types `credit_card`, `bank_account`, `login` with the fields above -> plus `note`, common
  fields, grouped bank address, question/answer pairs, TOTP; `credit_card` became `bank_card`
  (debit and gift cards too) and a login's `login` field became `username`
- each record has a unique id (uuid v3) -> UUID v4: v3 is derived from a name, so it isn't unique
- commands `add`, `query`, `del`, `list`, `create` -> grouped as `safe` (create, list, merge,
  export, import), `record` (add, list, show, edit, del, copy, otp, types), `keys` (list, add,
  remove, passwd, rekey), `query`, `context` and `ls`
- default to `$HOME/.digisafe/crt.pem` and `key.pem`, with options for alternates -> a
  passphrase-protected age key and optional hardware keys; `--dir` replaces the cert/key options
- adding a record prompts for the fields (empty allowed) or reads a JSON file
