# Changelog

## Unreleased

- A client connecting to a large document no longer waits for the hub to join
  its paragraphs one copy at a time: a document of 200 pages went from a
  second and a gigabyte of memory to a few milliseconds. Applying an edit
  across many paragraphs gains the same.
- A document with no node is sent as `[]`, not `null`: a client reads `null`
  as no document at all, and never opens an empty board.
- Dart: `DocSession.share` shows the others something of this person beside
  its selection — a pointer, a stroke being drawn — and `onShared` tells what
  a peer shares, as presence frames carry it. The hub already relayed them
  as they are. `share(now: true)` sends at once; `DocSession.authored` tells
  who made the edits of others.
- Dart: `DocSession(othersPrevail: true)` lets what others changed since stand
  against an undo or a redo that would overwrite it, for documents whose
  edits replace attributes rather than text.
- `MetaStore` and `MetaFile`: a format can keep something beside its file
  (the comments of a note) in a store that has room for it.
- Dart: `DocSession(drafts:)` keeps what the hub has not confirmed (the
  document it was editing and its edits, those in flight apart) in a
  `DocDrafts` store, and takes it up again when the document is opened
  next, offline or not: the hub tells what it had applied, and the rest is
  rebased over what changed meanwhile.
- Dart: when an undo gives new ids to the nodes it brings back, the
  attributes of text named `prefix.id` follow them.

## 0.3.0 — 2026-10-05

- `Follower.Follow` is told who made the edit it follows, and the
  documentation says what was already so: it applies its changes itself.
- `hello` tells the client the `id` it is known by; the Dart session keeps
  it as `DocSession.id`, to sign what it writes.

## 0.2.0 — 2026-10-04

- Dart: a tree keeps the text of its nodes paragraph by paragraph, in
  blocks, as the Go package does: a keystroke in a note of 1 MB no longer
  copies the note (from 22 ms to about 1 ms under `flutter test`).
  `Node.text` is made the first time it is read after an edit;
  `Node.textLength` tells its length without making it.

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
