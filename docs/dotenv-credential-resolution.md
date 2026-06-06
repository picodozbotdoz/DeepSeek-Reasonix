# Dotenv Credential Resolution

This document covers the `.env` file loading and credential resolution system in DeepSeek-Reasonix, which implements a three-tier cascade for API key and configuration discovery.

**Source:** `internal/config/dotenv.go`

---

## Overview

Reasonix needs to find API keys and other configuration values without requiring the user to set every variable manually in their shell environment. The dotenv loading system implements a three-tier cascade that balances project-specific overrides, user-level credentials, and legacy compatibility — all while ensuring that explicit environment variables always take precedence.

---

## Three-Tier Loading Cascade

The `loadDotEnv` function (and its generalization `loadDotEnvForRoot`) loads KEY=value files into the process environment in a specific order. The **first file to set a key wins**, because `loadDotEnvFile` only sets variables that are not already present in the environment.

### Tier 1: Project `.env`

**Path:** `./.env` (or `{root}/.env` for desktop tab workspaces)

The project-scoped `.env` file in the current working directory is loaded first. This allows project-specific overrides: a team might share a `.env` file (committed to version control) that sets a custom `REASONIX_MODEL` or a project-specific API endpoint, while individual developers override the API key in their user-level credentials file.

This tier is read-only for back-compatibility: it's a conventional location that many tools and Docker Compose setups already use. Reasonix reads it but does not write to it.

### Tier 2: User Credentials File

**Path:** `~/.config/reasonix/credentials.env`

This is where `reasonix setup` writes API keys. It is the primary location for personal credentials that should resolve from any directory, without ever touching a project's own `.env` file. This design ensures that:

- Running `reasonix setup` once makes the key available in every project
- Project `.env` files don't accidentally contain real API keys (a common security mistake)
- The credentials file follows the XDG Base Directory specification on all platforms

The path is obtained via `UserCredentialsPath()`, which resolves to the appropriate platform-specific config directory.

### Tier 3: Legacy Home `.env`

**Path:** `~/.env`

The legacy desktop fallback is loaded last. The Reasonix desktop app originally wrote keys to `~/.env` because the Wails framework's working directory is the user's home directory. This tier ensures backward compatibility for users who set up the desktop app before the `credentials.env` migration.

### Environment Variable Supremacy

Existing environment variables **always win** over all three tiers. The `loadDotEnvFile` function uses `os.LookupEnv` to check whether a key is already set before calling `os.Setenv`. This means:

- A user who sets `DEEPSEEK_API_KEY=...` in their shell profile overrides any `.env` file value
- CI/CD systems that inject secrets as environment variables take precedence
- The `--env` CLI flag (which sets environment variables before config loading) overrides `.env` files

---

## loadDotEnvForRoot

The `loadDotEnvForRoot` function generalizes the loading cascade for desktop tab workspaces, where each tab may have a different project root. When `root` is `.` (the CLI default), it behaves identically to `loadDotEnv()`. When `root` is a different directory (e.g., a desktop tab's workspace path), the project `.env` is loaded from `{root}/.env` instead of `./.env`.

This is important for the desktop app, where multiple tabs can be open simultaneously, each pointing to a different project. Each tab's controller loads its own project `.env` without interfering with other tabs.

---

## loadDotEnvFile Parsing

The `loadDotEnvFile` function implements lenient, zero-dependency parsing of `.env` files. The parsing rules are deliberately permissive to handle the wide variety of `.env` file formats in the wild:

### Line Processing

1. **Blank lines**: Lines that are empty after trimming are skipped
2. **Comment lines**: Lines starting with `#` are skipped
3. **`export` prefix**: Lines starting with `export ` (with trailing space) have the prefix stripped, supporting both `KEY=value` and `export KEY=value` syntax
4. **KEY=value split**: Lines are split on the first `=` character using `strings.Cut`
5. **Whitespace trimming**: Both key and value are trimmed of surrounding whitespace
6. **Quote stripping**: Outer quotes (`"` or `'`) are stripped from the value, supporting `KEY="value"` and `KEY='value'` syntax
7. **Empty keys**: Lines without an `=` sign or with an empty key after trimming are silently ignored

### Lenient Design

The parser does not raise errors for malformed lines — it simply skips them. This is intentional: `.env` files in real projects often contain comments, blank lines, and lines with unusual syntax from other tools. A strict parser would fail on these files and prevent Reasonix from starting.

### No Variable Expansion

The parser does **not** perform variable expansion (`${OTHER_VAR}` or `$OTHER_VAR`). This is a deliberate simplification: variable expansion introduces ordering dependencies (which variable is defined first?) and escaping complexity that would make the parser fragile. Users who need variable composition should use their shell profile instead.

---

## Debugging "Key Not Found" Errors

The three-tier cascade is critical for debugging "key not found" errors. When a user reports that their API key isn't being found, the resolution order matters:

1. **Check the environment**: Is the variable set in the shell? (`echo $DEEPSEEK_API_KEY`)
2. **Check the project `.env`**: Does `./.env` contain the key? Is it spelled correctly?
3. **Check the credentials file**: Does `~/.config/reasonix/credentials.env` contain the key?
4. **Check the legacy home `.env`**: Does `~/.env` contain the key? (desktop app users only)
5. **Check for typos**: The key name must match exactly — `DEEPSEEK_API_KEY` is not the same as `DEEPSEEK_APIKEY`

The `reasonix doctor` command includes provider key presence information (`key:present` or `key:missing`) in its output, which directly reflects the result of this cascade. If `doctor` shows `key:missing`, none of the three tiers provided the variable, and the user needs to run `reasonix setup` or set the environment variable manually.

---

## Concurrency Considerations

The dotenv loading is not protected by a mutex, because it runs exactly once during Reasonix startup (in `config.Load()`) before any concurrent access begins. The `os.Setenv` calls are safe in this context because there are no concurrent readers at load time. If the loading were to be called concurrently (e.g., from multiple desktop tabs), the `os.LookupEnv` + `os.Setenv` sequence would need synchronization to avoid TOCTOU races, but the current architecture ensures this never happens.
