# trame

The Flutter client of [trame](https://github.com/citadellefr/trame): documents
edited together in real time, offline edits included, against the Go hub.

```dart
import 'package:trame/trame.dart';

final session = DocSession(
  webSocketConnector((clientId) async => Uri.parse('wss://example.com/doc?client=$clientId')),
)..start();

session.replaceText('body', 0, 0, 'Hello');
```

A session shows local edits at once and rebases them over those of others;
`undo` and `redo` revert this person's edits only. `package:trame/testing.dart`
is a hub in memory for tests. See the
[repository README](https://github.com/citadellefr/trame#readme) for the
server side and the protocol.
