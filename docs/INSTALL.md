# Installing words-on-the-street

A release publishes one binary per platform **and** a `checksums.txt` file that
holds the SHA-256 of every binary. Verify the checksum before you run anything.
The install path never asks you to fetch a document and execute what it says.

## 1 · Download

From the latest release, download the binary for your platform and
`checksums.txt` into the same directory:

```
words-on-the-street_<os>_<arch>
checksums.txt
```

## 2 · Verify the checksum

On Linux:

```
sha256sum --check --ignore-missing checksums.txt
```

On macOS, if the GNU tools are not installed:

```
shasum -a 256 --check checksums.txt
```

On Windows, in PowerShell:

```
Get-FileHash .\words-on-the-street_windows_amd64.exe -Algorithm SHA256
```

`Get-FileHash` prints the digest. Compare it with the matching line in
`checksums.txt` by hand. If it differs, stop: the download is not the artifact
that was published for this release.

## 3 · Run

```
chmod +x words-on-the-street_<os>_<arch>
./words-on-the-street_<os>_<arch> version
```

## What the checksum proves, and what it does not

A matching checksum proves the bytes you run are the bytes published for the
release. It does not prove the release itself is trustworthy, and it does not
prove the binary is safe to run; that still rests on the source repository and
on who controls the release tag. A checksum is the floor, not the ceiling.

## Building from source

If you would rather build it yourself, no release artifact is involved:

```
go build -trimpath -o words-on-the-street ./cmd/words-on-the-street
```
