# Security

outfit installs binaries on developers' machines and swaps files in their repositories, so reports
about the installer, the updater, the release signing or anything that could overwrite a person's
bytes are welcome and taken seriously.

**Report privately** through GitHub: *Security* tab > *Report a vulnerability*
(<https://github.com/prosperity-solutions/outfit/security/advisories/new>). If that button is not there
yet, open an issue that says only that you have a security report (no details), and a maintainer will
arrange a private channel. Please do not put anything exploitable in a public issue. Include what you ran, what you expected and what happened; a
reproduction in a throwaway repository is the fastest way to a fix.

Which release is affected: the latest one (`outfit version`), and anything built from `main`. Older
releases are not patched; `outfit update` moves you to the newest.

How releases are made and checked, and what is and is not trusted, is in
[docs/security.md](docs/security.md) and [docs/release.md](docs/release.md). Every release carries a
SHA-256 list, a minisign signature of it (the public key is in `install.sh` and
`internal/update/keys.txt`) and signed build provenance (`gh attestation verify`).
