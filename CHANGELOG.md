# Changelog

## 0.1.1 — 2026-10-04

- A keystroke in a long paragraph no longer walks through it a dozen
  times: the length of an op is measured once, that of the edited paragraph
  deduced, and ASCII text counted eight bytes at a time. In a paragraph of
  1 MB, from 20 ms to 2.5 ms.

## 0.1.0 — 2026-10-02

- The hub, `ot` and the Dart session of L'Office, moved out of it to be shared
  with Bref. The hub takes a `Format`; `Text` serves plain text files.
  `trametest` and `package:trame/testing.dart` are what their tests connect
  with.
