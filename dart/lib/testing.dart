/// A hub in memory, for the tests of applications built on trame.
library;

import 'dart:async';
import 'dart:convert';

import 'trame.dart';

/// The Go hub's protocol, in memory: edits are rebased over the history,
/// applied and relayed. Frames wait in both directions until delivered, so
/// tests choose the order things cross the network in.
class FakeHub {
  FakeHub(String text)
    : doc = Tree.fromEdit(Edit([Change.create(Node(id: 'body', type: 'text', key: 'V', text: Delta([Op.insert('$text\n')])))]))!;

  /// A hub holding the document these nodes make.
  FakeHub.tree(Edit nodes) : doc = Tree.fromEdit(nodes)!;

  Tree doc;
  var version = 0;
  var epoch = 'e0';
  final history = <({Edit edit, int sid, String client, int n})>[];
  final acks = <String, int>{};
  final links = <FakeLink>[];
  var _sid = 0;
  var refuse = '';

  String get text => doc.text;

  Future<DocTransport> connect(String clientId) async {
    final link = FakeLink(this, clientId, ++_sid);
    Map<String, Object?> peer(FakeLink l) => {'sid': l.sid, 'id': l.client, 'name': 'Peer ${l.sid}'};
    link.frame({
      't': 'hello',
      'sid': link.sid,
      'id': clientId,
      'name': 'Peer ${link.sid}',
      'epoch': epoch,
      'v': version,
      'saved': 0,
      'peers': [for (final other in links) peer(other)],
    });
    for (final other in links) {
      other.frame({'t': 'join', 'peer': peer(link)});
    }
    links.add(link);
    return link;
  }

  /// Forgets everything but the document, as a server restarting does.
  Future<void> restart() async {
    epoch = 'e${int.parse(epoch.substring(1)) + 1}';
    history.clear();
    acks.clear();
    for (final link in [...links]) {
      await link.drop();
    }
  }

  void handle(FakeLink link, Map<String, Object?> msg) {
    switch (msg['t']) {
      case 'sync':
        link.synced = true;
        link.batch = msg['batch'] == true;
        final v = msg['v'] as int? ?? -1;
        final first = version - history.length;
        if (msg['epoch'] != epoch || v < first || v > version) {
          link.frame({'t': 'doc', 'v': version, 'ack': acks[link.client] ?? 0, 'd': doc.toEdit().toJson()});
          return;
        }
        for (var i = v - first; i < history.length; i++) {
          final e = history[i];
          link.frame(e.client == link.client
              ? {'t': 'ack', 'n': e.n, 'v': first + i + 1}
              : {'t': 'op', 'sid': e.sid, 'v': first + i + 1, 'd': e.edit.toJson()});
        }
        link.frame({'t': 'ready', 'v': version});
      case 'op':
        final n = msg['n']! as int;
        if (refuse.isNotEmpty) {
          link.frame({'t': 'nack', 'n': n, 'error': refuse});
          return;
        }
        var d = Edit.fromJson(msg['d'])!;
        final base = msg['v']! as int;
        for (final e in history.sublist(base - (version - history.length))) {
          d = e.edit.transform(d, thisFirst: true);
        }
        if (doc.apply(d) == null) throw StateError('$d does not apply');
        history.add((edit: d, sid: link.sid, client: link.client, n: n));
        acks[link.client] = n;
        version++;
        for (final other in links) {
          if (other != link && other.synced) {
            other.frame({'t': 'op', 'sid': link.sid, 'v': version, 'd': d.toJson()});
          }
        }
        link.frame({'t': 'ack', 'n': n, 'v': version});
      case 'eph':
        for (final other in links) {
          if (other != link) other.frame({'t': 'eph', 'sid': link.sid, 'd': msg['d']});
        }
    }
  }

  /// Delivers everything waiting, both ways, until nothing moves.
  Future<void> settle() async {
    for (var busy = true; busy;) {
      busy = false;
      for (final link in [...links]) {
        while (link.deliverUp() || link.deliverDown()) {
          busy = true;
        }
      }
      await Future<void>.delayed(Duration.zero);
    }
  }
}

class FakeLink implements DocTransport {
  FakeLink(this.hub, this.client, this.sid);

  final FakeHub hub;
  final String client;
  final int sid;
  var synced = false;
  var batch = false;
  final up = <Map<String, Object?>>[];
  final down = <Map<String, Object?>>[];
  final sent = <Map<String, Object?>>[];
  final _incoming = StreamController<String>();

  @override
  int? closeCode;

  @override
  String? closeReason;

  @override
  Stream<String> get messages => _incoming.stream;

  @override
  void send(String data) {
    final msg = jsonDecode(data) as Map<String, Object?>;
    sent.add(msg);
    up.add(msg);
  }

  @override
  Future<void> close() async {
    hub.links.remove(this);
    await _incoming.close();
  }

  void frame(Map<String, Object?> f) => down.add(f);

  bool deliverUp() {
    if (up.isEmpty || !hub.links.contains(this)) return false;
    hub.handle(this, up.removeAt(0));
    return true;
  }

  bool deliverDown() {
    if (down.isEmpty || _incoming.isClosed) return false;
    // what waits leaves as one message for a client that reads batches
    final frames = batch ? down.length : 1;
    _incoming.add(jsonEncode(frames > 1 ? down.sublist(0, frames) : down.first));
    down.removeRange(0, frames);
    return true;
  }

  /// Loses the connection and whatever was on its way.
  Future<void> drop([int? code, String? reason]) async {
    closeCode = code;
    closeReason = reason;
    up.clear();
    down.clear();
    hub.links.remove(this);
    await _incoming.close();
  }
}

/// The plain text file of these tests: the text of its node "body".
extension PlainText on Tree {
  String get text {
    final flow = this['body']!.text!.text;
    return flow.substring(0, flow.length - 1);
  }
}

extension PlainTextSession on DocSession {
  String get text => document.text;

  bool replace(int start, int end, String text) => replaceText('body', start, end, text);
}
