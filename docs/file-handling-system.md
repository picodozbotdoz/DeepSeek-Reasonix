# File Handling System

This document covers four file-related subsystems in DeepSeek-Reasonix: multi-encoding file detection and conversion, the attachment and clipboard image system, fuzzy file-name search for @-reference autocomplete, and cross-platform atomic file writing.

---

## File Encoding Detection

**Source:** `internal/fileutil/encoding/encoding.go`

The encoding package detects and converts file encodings for the built-in file tools (read, write, edit). It supports a range of encodings commonly encountered in real-world codebases, particularly those with CJK (Chinese, Japanese, Korean) content on Windows systems.

### Supported Encodings

The package defines the following encoding kinds:

| Kind | Description |
|------|-------------|
| `UTF8` | Plain UTF-8 without a BOM — the common case and the most widely used encoding in modern codebases |
| `UTF8BOM` | UTF-8 with a leading Byte Order Mark (EF BB BF), commonly produced by Windows Notepad and some IDEs |
| `UTF16LE` | UTF-16 Little-Endian with a BOM (FF FE), the native encoding of Windows NT's internal string representation |
| `UTF16BE` | UTF-16 Big-Endian with a BOM (FE FF), less common but sometimes seen in files exchanged with legacy mainframe systems |
| `UTF16LENoBOM` | UTF-16 Little-Endian without a BOM — common for source files saved by Windows tools that omit the BOM |
| `UTF16BENoBOM` | UTF-16 Big-Endian without a BOM, rare but included for completeness |
| `GB18030` | Chinese national standard charset (superset of GBK and GB2312), mandatory for software sold in China |
| `LossyUTF8` | Not valid UTF-8 and not valid GB18030 — decoded lossily as UTF-8 with replacement characters so the model sees something useful |

### Detection Cascade

The `Detect` function applies a cascade of increasingly permissive checks:

1. **BOM check** — Three-byte BOM (UTF-8), two-byte BOM (UTF-16 LE/BE). If a BOM is found, detection is immediately conclusive.

2. **BOM-less UTF-16 heuristic** — `DetectUTF16NoBOM` checks the NUL-byte distribution pattern. This must be tried before `utf8.Valid` because UTF-16 content's low bytes plus `0x00` high bytes are all valid UTF-8 code units; a naive check would incorrectly tag a UTF-16 file as UTF-8.

3. **Strict UTF-8** — If the bytes pass `utf8.Valid`, they are classified as UTF-8.

4. **GB18030** — The Chinese national standard encoding. It is a strict superset of GBK and rejects truly invalid byte sequences, so a successful decode is a reliable signal. The decoder comes from `golang.org/x/text/encoding/simplifiedchinese`.

5. **Lossy UTF-8** — The final fallback. If none of the above match, the data is treated as lossy UTF-8, meaning invalid byte sequences are replaced with the Unicode replacement character (U+FFFD). This ensures the model always sees *something* rather than refusing to open the file.

### DetectUTF16NoBOM

BOM-less UTF-16 detection is heuristic-based. The key insight is that ASCII-range text in UTF-16 encodes one byte of payload and one `0x00` byte per code unit, so the NUL bytes cluster on one parity (odd offsets for LE, even offsets for BE). The heuristic requires a **strong skew** — one parity must have at least 30% NULs while the other has less than 5% — so genuine binary files (NULs on both parities) and plain UTF-8 (no NULs) fall through safely.

The minimum input size is 16 bytes; shorter inputs are too ambiguous to classify reliably. The function examines an even-length window (by masking off the last bit of the length) so that parity counts are comparable between the two byte positions.

### DetectQuick

`DetectQuick` is a fast BOM-only check used for the peek-based binary rejection fast path. It inspects only the first 2–3 bytes for a BOM prefix. Files with a BOM (UTF-16, UTF-8 BOM) skip the NUL-byte check since `0x00` is normal in UTF-16 content. For non-BOM content, it returns `UTF8` and the caller is expected to fall through to the full `Detect` function after verifying no NUL bytes are present.

### Decode and Encode

The `Decode` function converts raw bytes from the detected encoding to UTF-8 for internal processing. The `Encode` function performs the reverse conversion, taking a UTF-8 string and producing bytes in the original encoding. Together, they provide **full round-trip support** that preserves the original encoding of a file across read → edit → write cycles.

Key implementation details:

- **UTF-8 BOM**: Decoding strips the 3-byte prefix; encoding prepends it back.
- **UTF-16 with BOM**: Decoding strips the 2-byte BOM, then decodes the code units; encoding prepends the BOM.
- **UTF-16 without BOM**: Decoding reads code units directly; encoding writes without a BOM to preserve the original byte representation.
- **GB18030**: Uses `golang.org/x/text/transform` for both encoding and decoding. Falls back to the raw input on error (which should not happen after a successful `Detect`).
- **UTF-8 / LossyUTF8**: Both pass through as-is, since Go strings can hold arbitrary bytes.

### Surrogate Pair Handling

The `utf16Decode` and `utf16Encode` helper functions properly handle UTF-16 surrogate pairs — code point sequences in the range U+D800–U+DFFF that encode supplementary plane characters (U+10000–U+10FFFF). Without this handling, characters like emoji (🎨 = U+1F3A8) and CJK Extension B characters would be corrupted during the round-trip.

---

## Attachment / Clipboard System

**Source:** `internal/control/attachments.go`

The attachment system handles image and file attachments for the Reasonix desktop app, including clipboard image capture and secure file storage.

### Image Support

Images are supported in four formats: PNG, JPEG, GIF, and WebP. The maximum size for image attachments is **10 MB**. Image type detection uses `http.DetectContentType` on the first 512 bytes of data, which provides reliable MIME type identification for all supported formats.

When an image is saved, its actual MIME type is detected from the binary content rather than trusting the declared type from the source. This prevents malformed or mislabeled images from causing display issues.

### File Support

Non-image file attachments support any extension matching the regex `^\.[a-z0-9]{1,12}$`. The maximum size for file attachments is **25 MB**. Extensions that don't match the safe pattern are silently replaced with `.bin` to prevent path traversal or executable upload attacks.

### Storage Layout

All attachments are stored under `.reasonix/attachments/` within the project root. File names are generated using the pattern:

```
clipboard-YYYYMMDD-HHMMSS.ffffff-NNNNNN.ext
```

Where `YYYYMMDD-HHMMSS.ffffff` is a microsecond-precision timestamp and `NNNNNN` is an atomic sequence counter. This ensures unique filenames even under concurrent access.

### Security Measures

The attachment system implements several security measures:

- **Symlink rejection**: Both the attachment directory itself and any path component within it are checked for symlinks. The `rejectSymlinkComponents` function walks each path component and verifies none are symbolic links, preventing symlink-based path traversal attacks.

- **Path traversal prevention**: The `cleanAttachmentPath` function verifies that the resolved path is relative, doesn't escape the `.reasonix/attachments/` root, and doesn't contain `..` components. Absolute paths are rejected outright.

- **Atomic file creation**: Files are created with `O_WRONLY|O_CREATE|O_EXCL` flags, which ensures atomic creation — if the file already exists, the creation fails. Up to 1000 attempts are made with different generated names before giving up.

- **TOCTOU protection**: For `SaveImageFile` and `SaveAttachmentFile`, the code performs three `Stat` calls (before opening, after opening, and after reading) and verifies they all return the same file identity via `os.SameFile`. This detects race conditions where the file is replaced between checks.

### Clipboard Capture

The `SaveClipboardImage` function captures the system clipboard as an image, with platform-specific implementations:

| Platform | Method |
|----------|--------|
| **macOS** | AppleScript — tries `«class PNGf»` then `«class JPEG»` from the clipboard |
| **Windows** | PowerShell 5.1 — uses `Get-Clipboard -Format Image`, saves to PNG in memory, returns base64 on stdout. PowerShell 5.1 (preinstalled on all Windows) is used instead of pwsh (Core) because the latter lacks `Get-Clipboard -Format Image`. The console window is hidden via `proc.HideWindow`. |
| **Linux** | Wayland (`wl-paste --type image/png`) then X11 (`xclip -selection clipboard -t image/png -o`) — tries Wayland first, falls back to X11 |

### Data URL Handling

- **SaveImageDataURL**: Decodes a `data:image/...;base64,...` data URL, detects the image MIME type from the binary content, and saves it as an attachment.
- **SaveAttachmentDataURL**: Decodes a `data:...;base64,...` data URL for non-image files, using only the original filename's extension for the stored file.
- **ImageDataURL**: The reverse operation — reads an attachment file, detects its MIME type, and encodes it as a `data:` URL for display in the frontend.

---

## Fuzzy File Search

**Source:** `internal/fileref/search.go`

The fuzzy file search subsystem provides bounded file-name search for `@`-reference autocomplete in the chat composer. When a user types `@foo`, the system finds files whose basename contains "foo" and presents them as completion candidates.

### Bounding and Performance

The search is aggressively bounded to keep interactive completion responsive even on large workspaces:

- **maxWalkEntries** (10,000): The walk stops after visiting 10,000 directory entries. This prevents sluggish searches on monorepo-scale projects.
- **minQueryLen** (2): Queries shorter than 2 characters are rejected outright, avoiding expensive broad searches that would return thousands of results.
- **limit parameter**: The caller specifies a maximum number of results; once that many matches are found, the walk terminates early with `filepath.SkipAll`.

### Directory Skipping

The `skipDirs` map excludes common generated and vendor directories that would produce unhelpful results:

- `.git` — version control internals
- `node_modules` — npm dependencies
- `dist` — build output
- `build` — build output

### Hidden File Handling

Hidden files and directories (those starting with `.`) are skipped **unless** the query itself starts with `.`, allowing users to find dotfiles like `.eslintrc` or `.env` when they explicitly search for them. This strikes a balance between noise reduction and discoverability.

### Matching and Sorting

Matching is case-insensitive on the **basename** of the file (not the full path). Only regular files are returned — directories, symlinks, and special files are excluded via an `Info().Mode().IsRegular()` check. Results are sorted alphabetically and returned as relative paths using `filepath.ToSlash` for cross-platform consistency.

### Path Separator Rejection

Queries containing `/` or `\` are rejected, preventing directory traversal attempts and keeping the search focused on filename matching rather than path matching.

---

## Atomic File Writing

**Source:** `internal/fileutil/atomicwrite.go`

The atomic file writing subsystem provides safe, cross-platform file replacement for configuration and session data.

### ReplaceFile

`ReplaceFile` renames a temporary file onto the destination, which is the standard atomic-write pattern on POSIX systems: `rename(2)` is atomic, so a crash at any point leaves either the old or the new file intact — never a partially written file.

### Windows EXDEV Fallback

On Windows, the simple rename approach can fail due to **encryption-software filter drivers**. Some Windows encryption products (e.g., certain enterprise disk encryption tools) intercept file operations and report a cross-device link error (`EXDEV`) for a `Rename` call within the same directory. This is a known issue where the filter driver's virtual filesystem doesn't implement same-directory renames correctly.

When `os.Rename` fails, `ReplaceFile` falls back to `copyOnto`, which:

1. Reads the source file's permissions via `os.Stat`
2. Reads the entire source file content via `os.ReadFile`
3. Writes the content to the destination via `os.WriteFile` with the source's permissions
4. Re-applies `os.Chmod` on the destination to ensure the permissions match exactly
5. Removes the temporary source file

### Permission Preservation

The `copyOnto` function takes special care to preserve file permissions. After `os.WriteFile`, it explicitly calls `os.Chmod(dest, info.Mode().Perm())` because `os.WriteFile` may keep an existing destination's mode rather than applying the mode from the source. This is critical for security-sensitive files: a temporary config file created with mode 0600 must not be widened to 0644 just because the destination happened to have that mode.

The double-chmod ensures that what the rename would have done (which atomically replaces the destination with the source, including its mode) is also what the copy fallback does.

### Critical Use Cases

`ReplaceFile` is used throughout Reasonix for:

- **Configuration writing** — The TOML config file is written to a temp file first, then atomically replaced
- **Session persistence** — JSONL session files are written atomically to prevent corruption on crash
- **Memory file updates** — The in-place editor saves via atomic replacement
- **Credential storage** — The credentials.env file is written atomically to protect API keys
