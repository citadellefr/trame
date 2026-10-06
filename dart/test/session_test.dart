import 'dart:async';
import 'dart:math';

import 'package:flutter_test/flutter_test.dart';
import 'package:trame/testing.dart';
import 'package:trame/trame.dart';

void main() {
  group('drafts', _draftTests);

  late FakeHub hub;
  final sessions = <DocSession>[];

  Future<DocSession> open([String? clientId]) async {
    final session = DocSession(hub.connect, clientId: clientId)..start();
    sessions.add(session);
    await pumpEventQueue();
    await hub.settle();
    return session;
  }

  setUp(() => hub = FakeHub('one\ntwo'));

  tearDown(() {
    for (final s in sessions) {
      s.dispose();
    }
    sessions.clear();
  });

  test('comes online with the document', () async {
    final s = await open('me');
    expect(s.status, DocStatus.online);
    expect(s.id, 'me');
    expect(s.text, 'one\ntwo');
    expect(s.saved, isTrue);
  });

  test('sends one edit at a time and composes the next ones', () async {
    final s = await open();
    final link = hub.links.single;
    s.replace(0, 0, 'a');
    s.replace(1, 1, 'b');
    s.replace(2, 2, 'c');
    expect(s.text, 'abcone\ntwo');
    expect(link.up.map((m) => m['d']), [
      [_txt([{'i': 'a'}])],
    ]);
    expect(s.saved, isFalse);
    await hub.settle();
    expect(link.sent.where((m) => m['t'] == 'op').map((m) => m['d']), [
      [_txt([{'i': 'a'}])],
      [_txt([{'r': 1}, {'i': 'bc'}])],
    ]);
    expect(hub.text, 'abcone\ntwo');
  });

  test('concurrent editors converge', () async {
    final random = Random(3);
    final editors = [for (var i = 0; i < 3; i++) await open()];
    for (var step = 0; step < 400; step++) {
      final s = editors[random.nextInt(editors.length)];
      final length = s.text.length;
      final at = random.nextInt(length + 1);
      switch (random.nextInt(3)) {
        case 0 when at < length:
          s.replace(at, min(length, at + 1 + random.nextInt(3)), '');
        default:
          s.replace(at, at, ['x', 'yz', '\n', 'é'][random.nextInt(4)]);
      }
      final link = hub.links[random.nextInt(hub.links.length)];
      if (random.nextBool()) {
        link.deliverUp();
      } else {
        link.deliverDown();
      }
      await Future<void>.delayed(Duration.zero);
    }
    await hub.settle();
    for (final s in editors) {
      expect(s.text, hub.text);
      expect(s.saved, isFalse);
    }
  });

  for (var seed = 0; seed < 10; seed++) {
    test('concurrent editors of nodes converge, $seed', () => _nodesConverge(seed, open, () => hub));
  }

  test('edits made offline reach the document on reconnection', () async {
    final a = await open('a');
    final b = await open('b');
    await hub.links.first.drop();
    await pumpEventQueue();
    expect(a.status, DocStatus.offline);

    a.replace(3, 3, ' (a)');
    b.replace(0, 0, 'B: ');
    await hub.settle();
    a.retry();
    await pumpEventQueue();
    await hub.settle();
    expect(hub.text, 'B: one (a)\ntwo');
    expect(a.text, hub.text);
    expect(b.text, hub.text);
  });

  test('an edit whose acknowledgement was lost is not applied twice', () async {
    final a = await open('a');
    final b = await open('b');
    a.replace(0, 0, 'x');
    hub.links.first.deliverUp();
    await hub.links.first.drop();
    await pumpEventQueue();
    b.replace(7, 7, '!');
    await hub.settle();
    a.retry();
    await pumpEventQueue();
    await hub.settle();
    expect(hub.text, 'xone\ntwo!');
    expect(a.text, hub.text);
  });

  test('after a server restart, local edits are rebased on the text', () async {
    final a = await open('a');
    final b = await open('b');
    await hub.links.first.drop();
    await pumpEventQueue();
    a.replace(7, 7, ' (a)');
    b.replace(0, 3, 'ONE');
    await hub.settle();
    await hub.restart();
    await pumpEventQueue();
    a.retry();
    b.retry();
    await pumpEventQueue();
    await hub.settle();
    expect(hub.text, 'ONE\ntwo (a)');
    expect(a.text, hub.text);
    expect(b.text, hub.text);
  });

  test('undo reverts only its own edits', () async {
    final a = await open();
    final b = await open();
    a.replace(0, 0, 'A');
    await hub.settle();
    b.replace(4, 4, 'B');
    await hub.settle();
    expect(a.text, 'AoneB\ntwo');
    a.undo();
    await hub.settle();
    expect(b.text, 'oneB\ntwo');
    a.redo();
    await hub.settle();
    expect(b.text, 'AoneB\ntwo');
    expect(a.canRedo, isFalse);
  });

  test('typing is undone in one step', () async {
    final s = await open();
    for (final c in 'hello'.split('')) {
      s.replace(s.text.length, s.text.length, c);
    }
    s.undo();
    expect(s.text, 'one\ntwo');
  });

  test('a refused edit is rolled back, the later ones kept', () async {
    final s = await open();
    final reasons = <String>[];
    s.rejections.listen(reasons.add);
    hub.refuse = 'read-only access';
    s.replace(0, 0, 'x');
    s.replace(1, 1, 'y');
    hub.links.single.deliverUp();
    hub.refuse = '';
    await hub.settle();
    expect(reasons, ['read-only access']);
    expect(s.text, 'yone\ntwo');
    expect(hub.text, 'yone\ntwo');
  });

  test('never deletes the last paragraph mark', () async {
    final s = await open();
    bool edit(Delta d) => s.edit(Edit([Change.text('body', d)]));
    expect(edit(Delta()..retain(7)..delete(1)), isFalse);
    expect(edit(Delta()..retain(8)..insert('x')), isFalse);
    expect(edit(Delta()..retain(7)..delete(1)..insert('\n')), isFalse);
    expect(s.replace(0, 7, ''), isTrue);
    expect(s.text, '');
  });

  test('shares what is not a selection, told as it arrived', () async {
    final a = await open();
    final b = await open();
    final told = <(int, Map<String, Object?>)>[];
    a.onShared = (peer, data) => told.add((peer.sid, data));
    b.share({'c': [3, 4]});
    b.share({'d': null});
    await Future<void>.delayed(const Duration(milliseconds: 60));
    await hub.settle();
    expect(told, hasLength(1));
    expect(told.single.$1, a.peers.single.sid);
    expect(told.single.$2, {'c': [3, 4], 'd': null});

    // a selection travels beside it, in the same frame
    told.clear();
    b.share({'c': null});
    b.select(const DocSelection('body', 1, 2));
    await Future<void>.delayed(const Duration(milliseconds: 60));
    await hub.settle();
    expect(told.single.$2, {'c': null});
    expect(a.peers.single.selection, const DocSelection('body', 1, 2));
    unawaited(b.stop());
  });

  test('shares at once on demand, and tells who made what others did', () async {
    final a = await open();
    final b = await open();
    final told = <Map<String, Object?>>[];
    a.onShared = (_, data) => told.add(data);
    final authors = <int?>[];
    final sub = a.authored.listen((e) => authors.add(e.author?.sid));
    addTearDown(sub.cancel);
    b.share({'d': 1}, now: true);
    b.share({'d': 2}, now: true);
    await hub.settle();
    expect(told, [
      {'d': 1},
      {'d': 2},
    ]);

    b.replace(0, 0, 'x');
    await hub.settle();
    expect(authors, [a.peers.single.sid]);
    unawaited(b.stop());
  });

  test('shares nothing while the connection is down', () async {
    final a = await open();
    final b = await open();
    final told = <Map<String, Object?>>[];
    a.onShared = (_, data) => told.add(data);
    await hub.links.firstWhere((l) => l.client == b.clientId).drop();
    b.share({'c': [1, 1]});
    await Future<void>.delayed(const Duration(milliseconds: 60));
    await hub.settle();
    expect(told, isEmpty);
    unawaited(b.stop());
  });

  test('shows where others are, following the text', () async {
    final a = await open();
    final b = await open();
    b.select(const DocSelection('body', 4, 7));
    await Future<void>.delayed(const Duration(milliseconds: 60));
    await hub.settle();
    expect(a.peers.single.selection, const DocSelection('body', 4, 7));
    a.replace(0, 0, '>> ');
    expect(a.peers.single.selection, const DocSelection('body', 7, 10));
    unawaited(b.stop());
  });
}

Map<String, Object?> _txt(List<Object?> delta) => {'o': 'txt', 'id': 'body', 'x': delta};

Future<void> _nodesConverge(int seed, Future<DocSession> Function() open, FakeHub Function() hub) async {
  final random = Random(seed);
  final editors = [for (var i = 0; i < 3; i++) await open()];
  var next = 0;
  for (var step = 0; step < 400; step++) {
    final s = editors[random.nextInt(editors.length)];
    final nodes = s.document.nodes.toList();
    final node = nodes[random.nextInt(nodes.length)];
    s.edit(Edit([
      switch (random.nextInt(6)) {
        0 => Change.create(Node(id: 'n${next++}', type: 't', parent: node.id, key: 'V', text: Delta([const Op.insert('x\n')]))),
        1 when node.id != 'body' => Change.delete(node.id),
        2 => Change.set(node.id, key: keyBetween('', node.key), attributes: {'x': random.nextInt(3)}),
        _ when node.text != null => Change.text(node.id, Delta()..insert('ab')),
        _ => Change.set(node.id, attributes: {'y': null}),
      },
    ]));
    if (random.nextInt(4) == 0) s.undo();
    final link = hub().links[random.nextInt(hub().links.length)];
    if (random.nextBool()) {
      link.deliverUp();
    } else {
      link.deliverDown();
    }
    await Future<void>.delayed(Duration.zero);
  }
  await hub().settle();
  for (final s in editors) {
    expect(s.document.toEdit(), hub().doc.toEdit());
  }
}

class _Drafts implements DocDrafts {
  String? kept;

  @override
  Future<String?> load() async => kept;

  @override
  Future<void> save(String draft) async => kept = draft;

  @override
  Future<void> clear() async => kept = null;
}

void _draftTests() {
  late FakeHub hub;
  setUp(() => hub = FakeHub('one\ntwo'));

  test('edits unconfirmed when the app closes are found again and reach the document', () async {
    final drafts = _Drafts();
    final a = DocSession(hub.connect, clientId: 'a', drafts: drafts, draftDelay: const Duration(milliseconds: 20))..start();
    await pumpEventQueue();
    await hub.settle();
    expect(drafts.kept, isNull);
    await hub.links.single.drop();
    await pumpEventQueue();
    a.replace(3, 3, ' (a)');
    await Future<void>.delayed(const Duration(milliseconds: 60));
    expect(drafts.kept, isNotNull);
    a.dispose();

    final b = DocSession(hub.connect, drafts: drafts, draftDelay: const Duration(milliseconds: 20))..start();
    await pumpEventQueue();
    expect(b.text, 'one (a)\ntwo');
    expect(b.clientId, 'a');
    await hub.settle();
    expect(hub.text, 'one (a)\ntwo');
    await Future<void>.delayed(const Duration(milliseconds: 60));
    expect(drafts.kept, isNull);
    b.dispose();
  });

  test('a draft is rebased over what others did meanwhile, and what the hub applied is not applied twice', () async {
    final drafts = _Drafts();
    final a = DocSession(hub.connect, clientId: 'a', drafts: drafts, draftDelay: const Duration(milliseconds: 20))..start();
    await pumpEventQueue();
    final c = DocSession(hub.connect, clientId: 'c')..start();
    await pumpEventQueue();
    await hub.settle();
    a.replace(0, 0, 'x');
    hub.links.first.deliverUp();
    await hub.links.first.drop();
    await pumpEventQueue();
    a.replace(4, 4, '!');
    c.replace(0, 0, 'C: ');
    await hub.settle();
    await Future<void>.delayed(const Duration(milliseconds: 60));
    a.dispose();

    final b = DocSession(hub.connect, drafts: drafts)..start();
    await pumpEventQueue();
    await hub.settle();
    expect(hub.text, 'xC: one!\ntwo');
    expect(b.text, hub.text);
    b.dispose();
    c.dispose();
  });
}
