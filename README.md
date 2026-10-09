# trame

**The hub of collaborative editors, for Go servers and Flutter apps.**

trame keeps documents edited by several people at once in step: a Go hub
orders and relays their edits and saves the document through your own
storage, and a Flutter session shows each person their own edits at once and
rebases them over those of others. It is what
[L'Office](https://github.com/citadellefr/loffice) and
[Bref](https://github.com/citadellefr/bref) are built on, developed by
[Citadelle](https://github.com/citadellefr) and released under the MIT
license.

## Principles

- **Nobody waits.** A client applies its edits as it makes them and rebases
  them over what arrives; the hub rebases late edits over those it applied
  since. Everyone converges on the same document.
- **The format is yours.** A `Format` reads a file into a tree of nodes, says
  which edits it takes and writes it back. Plain text files come with trame.
- **Your storage, your rules.** The hub calls a two-method `Store`; the
  connection reaches it already authenticated and authorized.
- **Small servers.** No dependency outside the Go standard library.

## Packages

| Package | Role |
|---|---|
| [`trame`](.) | The hub: one room per open document, edits rebased and relayed to everyone connected, cursors relayed, saves after a pause. `Text` is the format of plain text files. |
| [`ot`](ot) | Edits and how concurrent edits are reconciled. A document is a tree of nodes, each with a type, attributes and possibly text or a grid of cells; text is a flow of characters and paragraph marks, changed by deltas. |
| [`trametest`](trametest) | A connection and a store in memory, and a client that reads the frames of the protocol, for the tests of a hub or of an application serving one. |
| [`dart`](dart) | The Flutter package: the same `ot` algorithms, checked against the vectors of `testdata/ot`, and `DocSession`, the client of the hub. `package:trame/testing.dart` is a hub in memory. |

## Usage

```go
hub := trame.NewHub(store, trame.Text, trame.Options{})

// in the handler of an authorized WebSocket
err := hub.Serve(ctx, conn, "notes/todo.txt", trame.Peer{ID: "42", Name: "Alice", Client: clientID})
```

```dart
final session = DocSession(
  webSocketConnector((clientId) async => Uri.parse('wss://example.com/doc?client=$clientId')),
)..start();
```

## Protocol

A client connects over a WebSocket the host application has authorized, and
exchanges JSON frames with the hub:

| From | Frame | Meaning |
|---|---|---|
| hub | `hello` | who the client is (`sid`, `id`, `name`), who else is there, which stay in memory of the document (`epoch`) |
| client | `sync` | the `epoch` and revision `v` of the document it holds, if any, and `batch` when it reads several frames sent as one message, a JSON array of them |
| hub | `doc` | the whole document at revision `v`, as the edit `d` that creates its nodes, and `ack`, the last edit of this client applied |
| hub | `op`, `ack` … `ready` | or else the edits it missed since `v`, its own acknowledged |
| client | `op` | an edit `d`, numbered `n`, made on revision `v` |
| hub | `op` | someone's edit, rebased, with the revision `v` it made |
| hub | `ack`, `nack` | the client's edit `n` applied as revision `v`, or refused and why |
| both | `eph` | cursors and selections, relayed as they are |
| hub | `join`, `leave`, `saved`, `error` | people coming and going, saves and why one failed |

An edit is a list of changes applied together:

```json
[{"o":"new","id":"s2","t":"slide","k":"V","a":{"hidden":true}},
 {"o":"set","id":"s1","k":"F","a":{"hidden":null}},
 {"o":"txt","id":"title","x":[{"r":5},{"i":"!"}]},
 {"o":"del","id":"s3"}]
```

Nodes are ordered among their siblings by key (`k`), then id. A change to a
node that no longer exists does nothing, and an id is never used again once
its node is deleted. A client keeps one edit in flight and holds the next ones
until it is acknowledged.

## Tests

```sh
go test -race ./...
go test ./ot -run Vectors -update   # after changing the ot algorithms
(cd dart && flutter test)          # replays the same vectors
go test -run '^$' -bench Relay .
go test -run '^$' -bench Crowd -benchtime 25x .   # 128 people typing
```
