import 'dart:async';
import 'dart:convert';
import 'dart:math' as math;

import 'package:flutter/foundation.dart';
import 'package:web_socket_channel/web_socket_channel.dart';

import 'ot/delta.dart';
import 'ot/grid.dart';
import 'ot/tree.dart';

/// The text link to the server, behind an interface so that sessions can be
/// tested without one.
abstract interface class DocTransport {
  Stream<String> get messages;
  int? get closeCode;
  String? get closeReason;
  void send(String data);
  Future<void> close();
}

/// Opens a transport for the session whose client id is given; called again
/// on every reconnection.
typedef DocConnector = Future<DocTransport> Function(String clientId);

/// A connector over WebSocket. [url] is asked again on every connection, so
/// it may carry a short-lived ticket.
DocConnector webSocketConnector(Future<Uri> Function(String clientId) url) => (clientId) async {
  final channel = WebSocketChannel.connect(await url(clientId));
  await channel.ready;
  return _WebSocketTransport(channel);
};

class _WebSocketTransport implements DocTransport {
  _WebSocketTransport(this._channel);

  final WebSocketChannel _channel;

  @override
  Stream<String> get messages =>
      _channel.stream.map((m) => m is String ? m : utf8.decode(m as List<int>));

  @override
  int? get closeCode => _channel.closeCode;

  @override
  String? get closeReason => _channel.closeReason;

  @override
  void send(String data) => _channel.sink.add(data);

  @override
  Future<void> close() async => _channel.sink.close();
}

/// Where a session keeps what the hub has not confirmed of a document, so that
/// closing the app offline loses nothing: the next session for the same
/// document finds it with [load] and rebases it over what changed meanwhile.
abstract interface class DocDrafts {
  Future<String?> load();
  Future<void> save(String draft);
  Future<void> clear();
}

enum DocStatus { connecting, online, offline, closed }

/// Why the server ended the session: the document could not be opened, or
/// access to it was withdrawn. [reason] is the server's own words.
class DocClosed implements Exception {
  const DocClosed(this.code, this.reason);

  final int code;
  final String reason;

  @override
  String toString() => reason;
}

/// Where someone's selection is: a range of the text of a node, base then
/// extent, or cells of a sheet.
@immutable
class DocSelection {
  const DocSelection(this.node, this.base, this.extent) : cells = null;

  const DocSelection.collapsed(this.node, int offset) : base = offset, extent = offset, cells = null;

  /// Cells of the sheet [node], from [top] [left] to [bottom] [right].
  DocSelection.cells(this.node, int top, int left, int bottom, int right)
    : base = 0,
      extent = 0,
      cells = List.unmodifiable([top, left, bottom, right]);

  final String node;
  final int base;
  final int extent;

  /// The rows and columns of the cells selected: top, left, bottom, right.
  final List<int>? cells;

  int get start => math.min(base, extent);

  int get end => math.max(base, extent);

  static DocSelection? fromJson(Object? json) {
    if (json is! Map<String, Object?>) return null;
    final node = json['n'], base = json['b'], extent = json['e'], cells = json['c'];
    if (node is! String) return null;
    if (cells is List<Object?> && cells.length == 4 && cells.every((c) => c is int)) {
      final c = cells.cast<int>();
      return DocSelection.cells(node, c[0], c[1], c[2], c[3]);
    }
    return base is int && extent is int ? DocSelection(node, base, extent) : null;
  }

  Map<String, Object?> toJson() => cells != null ? {'n': node, 'c': cells} : {'n': node, 'b': base, 'e': extent};

  /// This selection once [edit] is made; null when its node went away.
  DocSelection? moved(Edit edit, Tree after, {required bool own}) {
    if (after[node] == null) return null;
    final cells = this.cells;
    if (cells != null) {
      var (top, left, bottom, right) = (cells[0], cells[1], cells[2], cells[3]);
      for (final c in edit.changes) {
        if (c.id != node || (c.kind != ChangeKind.insert && c.kind != ChangeKind.remove)) continue;
        final n = c.kind == ChangeKind.insert ? c.n : -c.n;
        int move(int i, int limit) => movedIndex(i, c.at, n, limit) ?? math.max(1, c.at - 1);
        if (c.dim == dimRows) {
          top = move(top, maxRows);
          bottom = move(bottom, maxRows);
        } else {
          left = move(left, maxCols);
          right = move(right, maxCols);
        }
      }
      if (top == cells[0] && left == cells[1] && bottom == cells[2] && right == cells[3]) return this;
      return DocSelection.cells(node, top, left, math.max(top, bottom), math.max(left, right));
    }
    var (b, e) = (base, extent);
    for (final c in edit.changes) {
      if (c.kind != ChangeKind.text || c.id != node) continue;
      b = c.text!.transformPosition(b, thisFirst: own);
      e = c.text!.transformPosition(e, thisFirst: own);
    }
    return b == base && e == extent ? this : DocSelection(node, b, e);
  }

  @override
  bool operator ==(Object other) =>
      other is DocSelection &&
      other.node == node &&
      other.base == base &&
      other.extent == extent &&
      listEquals(other.cells, cells);

  @override
  int get hashCode => Object.hash(node, base, extent, cells == null ? null : Object.hashAll(cells!));

  @override
  String toString() => cells != null ? '$node$cells' : '$node[$base, $extent]';
}

class DocPeer {
  DocPeer._(this.sid, this.id, this.name, this.readOnly);

  final int sid;
  final String id;
  final String name;
  final bool readOnly;

  DocSelection? _selection;

  /// Where the peer's selection is, if it is in the document.
  DocSelection? get selection => _selection;
}

/// One person's connection to a document: keeps [document] in step with the
/// server, sends local edits, reconnects when the link drops and keeps the
/// history [undo] and [redo] walk through.
///
/// Local edits show at once. One is in flight to the server at a time; the
/// next ones wait behind it, offline included, and all of them are rebased
/// over the edits of others as those arrive.
class DocSession extends ChangeNotifier {
  DocSession(this._connect, {String? clientId, this.drafts, this.draftDelay = const Duration(seconds: 2)}) : _clientId = clientId ?? randomId();

  static const _historyLimit = 500;
  static const _typingPause = Duration(milliseconds: 800);
  static const _presenceEvery = Duration(milliseconds: 50);
  static const _backoff = [1, 2, 5, 10, 20, 30];

  final DocConnector _connect;
  String _clientId;

  /// Where what the hub has not confirmed is kept, if anywhere.
  final DocDrafts? drafts;

  /// How long unconfirmed edits wait before the draft is written, which
  /// then follows at the same pace.
  final Duration draftDelay;

  String get clientId => _clientId;

  /// Notifies selection changes of peers, far more frequent than the others.
  final Listenable presence = _Presence();

  final _peers = <int, DocPeer>{};
  final _undo = <Edit>[];
  final _redo = <Edit>[];
  final _changes = StreamController<Edit>.broadcast(sync: true);
  final _rejections = StreamController<String>.broadcast();

  DocStatus _status = DocStatus.connecting;
  Object? _failure;
  String? _saveError;
  var _readOnly = false;
  var _id = '';
  var _name = '';
  var _running = false;
  var _disposed = false;
  var _attempt = 0;
  var _savedVersion = 0;
  var _ackVersion = 0;
  DocTransport? _transport;
  StreamSubscription<String>? _subscription;
  Timer? _retry;

  Tree? _doc;
  Tree _confirmed = Tree();
  var _rev = 0;
  String? _epoch;
  String? _joined;
  var _synced = false;
  var _n = 0;
  Edit? _inflight;
  var _pending = 0;
  var _sent = false;
  Edit? _buffer;
  DateTime? _lastTyping;
  Timer? _draftTimer;
  var _draftWritten = false;
  var _draftRead = false;

  DocSelection? _selection;
  var _selectionDirty = false;
  Timer? _presenceTimer;

  DocStatus get status => _status;

  /// Why the session is offline or closed, when it knows.
  Object? get failure => _failure;

  /// Why the last save failed, until one succeeds.
  String? get saveError => _saveError;

  bool get readOnly => _readOnly;

  /// Who the server knows this client as: the id of a [DocPeer], which
  /// signs what it writes, and the name of its comments.
  String get id => _id;

  String get name => _name;

  Iterable<DocPeer> get peers => _peers.values;

  /// Whether the document arrived: nothing can be edited before.
  bool get loaded => _doc != null;

  /// The document as shown, local edits included. It changes in place:
  /// [changes] tells how.
  Tree get document => _doc ?? Tree();

  /// Whether every local edit reached the file.
  bool get saved => _pending == 0 && _buffer == null && _savedVersion >= _ackVersion && _saveError == null;

  bool get canUndo => _undo.isNotEmpty;

  bool get canRedo => _redo.isNotEmpty;

  /// Every change made to [document], local or not, once it is made.
  Stream<Edit> get changes => _changes.stream;

  /// Why the server refused a local edit, which has been rolled back.
  Stream<String> get rejections => _rejections.stream;

  void start() {
    if (_running || _disposed) return;
    _running = true;
    _failure = null;
    _attempt = 0;
    unawaited(_open());
  }

  /// Reconnects now, after a close or while waiting for the next retry.
  void retry() {
    _retry?.cancel();
    _retry = null;
    if (_running && (_transport != null || _status == DocStatus.connecting)) return;
    _running = false;
    start();
  }

  Future<void> stop() async {
    _running = false;
    _retry?.cancel();
    _presenceTimer?.cancel();
    final transport = _transport;
    _transport = null;
    await _subscription?.cancel();
    _subscription = null;
    await transport?.close();
  }

  @override
  void dispose() {
    _disposed = true;
    _draftTimer?.cancel();
    unawaited(stop());
    unawaited(_changes.close());
    unawaited(_rejections.close());
    (presence as _Presence).dispose();
    super.dispose();
  }

  /// Replaces the text of [node] between [start] and [end]; each "\n" in
  /// [text] starts a paragraph.
  bool replaceText(String node, int start, int end, String text) => edit(Edit([
    Change.text(
      node,
      Delta()
        ..retain(start)
        ..delete(end - start)
        ..insert(text),
    ),
  ]));

  /// Makes a local edit, which [undo] reverts. Typing is undone in bursts,
  /// as Office does.
  bool edit(Edit edit) {
    final doc = _doc;
    if (doc == null || _readOnly || edit.isEmpty) return false;
    final inverse = doc.apply(edit);
    if (inverse == null) return false;
    final now = DateTime.now();
    final typing = _lastTyping != null && now.difference(_lastTyping!) < _typingPause && _undo.isNotEmpty;
    if (typing) {
      _undo.last = inverse.compose(_undo.last);
    } else {
      _undo.add(inverse);
      if (_undo.length > _historyLimit) _undo.removeAt(0);
    }
    _lastTyping = now;
    _redo.clear();
    _commit(edit);
    return true;
  }

  void undo() => _travel(_undo, _redo);

  void redo() => _travel(_redo, _undo);

  /// Applies the top of [from], skipping what the edits of others emptied
  /// or made impossible, and keeps its inverse on [to].
  void _travel(List<Edit> from, List<Edit> to) {
    final doc = _doc;
    if (doc == null || _readOnly) return;
    _lastTyping = null;
    while (from.isNotEmpty) {
      final edit = _renewed(from.removeLast());
      if (edit.isEmpty) continue;
      final inverse = doc.apply(edit);
      if (inverse == null) continue;
      to.add(inverse);
      _commit(edit);
      return;
    }
    notifyListeners();
  }

  /// [edit] with the nodes it creates given new ids, and the rest of the
  /// history following: undoing a deletion must not bring an id back, or
  /// what others did meanwhile to the deleted node would apply to the new.
  Edit _renewed(Edit edit) {
    final names = {
      for (final c in edit.changes)
        if (c.kind == ChangeKind.create) c.id: randomId(),
    };
    if (names.isEmpty) return edit;
    for (final stack in [_undo, _redo]) {
      for (var i = 0; i < stack.length; i++) {
        stack[i] = stack[i].renamed(names);
      }
    }
    return edit.renamed(names);
  }

  /// Sends a local edit already applied to the document.
  void _commit(Edit edit) {
    _moved(edit, author: null);
    if (_pending == 0 && _synced) {
      _send(edit);
    } else {
      _buffer = _buffer?.compose(edit) ?? edit;
    }
    _draftChanged();
    notifyListeners();
  }

  /// Keeps the draft a moment after the hub has failed to confirm what this
  /// client holds, and drops it once everything is.
  void _draftChanged() {
    if (drafts == null || _doc == null) return;
    _draftTimer ??= Timer(draftDelay, () => unawaited(_writeDraft()));
  }

  Future<void> _writeDraft() async {
    _draftTimer = null;
    final store = drafts;
    if (store == null || _disposed) return;
    final unconfirmed = _pending != 0 || _buffer != null;
    try {
      if (unconfirmed) {
        await store.save(jsonEncode({
          'client': _clientId,
          'n': _n,
          'base': _confirmed.toEdit().toJson(),
          if (_inflight != null) 'inflight': _inflight!.toJson(),
          if (_buffer != null) 'buffer': _buffer!.toJson(),
        }));
        _draftWritten = true;
      } else if (_draftWritten) {
        await store.clear();
        _draftWritten = false;
      }
    } on Object catch (error) {
      if (!_disposed) _rejections.add('$error');
    }
  }

  /// Takes up what an earlier run left unconfirmed, before anything is
  /// received: the document it was editing, its edits in flight included,
  /// which the hub tells apart from those it has applied.
  Future<void> _restore() async {
    final store = drafts;
    if (store == null || _draftRead) return;
    _draftRead = true;
    try {
      final raw = await store.load();
      if (raw == null || _doc != null || _disposed) return;
      final json = jsonDecode(raw);
      if (json is! Map<String, Object?>) throw const FormatException('draft');
      final nodes = Edit.fromJson(json['base']);
      final base = nodes == null ? null : Tree.fromEdit(nodes);
      if (base == null) throw const FormatException('draft');
      final inflight = json['inflight'] == null ? null : Edit.fromJson(json['inflight']);
      final buffer = json['buffer'] == null ? null : Edit.fromJson(json['buffer']);
      final doc = base.copy();
      if (inflight != null && doc.apply(inflight) == null || buffer != null && doc.apply(buffer) == null) {
        throw const FormatException('draft');
      }
      _clientId = '${json['client']}';
      _n = _int(json['n']);
      _confirmed = base;
      _doc = doc;
      _inflight = inflight;
      _pending = inflight == null ? 0 : _n;
      _buffer = buffer;
      _draftWritten = true;
      _changes.add(doc.toEdit());
      notifyListeners();
    } on Object catch (error) {
      if (!_disposed) _rejections.add('$error');
    }
  }

  /// Where this person's selection is, null once it left the document.
  void select(DocSelection? selection) {
    if (selection == _selection) return;
    _selection = selection;
    _selectionDirty = true;
    if (_presenceTimer?.isActive ?? false) return;
    _presenceTimer = Timer(_presenceEvery, _flushPresence);
  }

  void _flushPresence() {
    final transport = _transport;
    if (transport == null || !_synced || !_selectionDirty) return;
    _selectionDirty = false;
    transport.send(jsonEncode({
      't': 'eph',
      'd': {'s': _selection?.toJson()},
    }));
  }

  /// Applies a change others made to the document shown.
  void _show(Edit edit, {required int author}) {
    _doc!.apply(edit);
    _moved(edit, author: author);
  }

  /// Moves everything that points into the document over [edit], made by
  /// [author], null for this one.
  void _moved(Edit edit, {required int? author}) {
    final doc = _doc!;
    for (final peer in _peers.values) {
      peer._selection = peer._selection?.moved(edit, doc, own: peer.sid == author);
    }
    _changes.add(edit);
  }

  /// Rebases the undo and redo stacks over a change others made.
  void _rebaseHistory(Edit change) {
    for (final stack in [_undo, _redo]) {
      var c = change;
      for (var i = stack.length - 1; i >= 0; i--) {
        final entry = stack[i];
        stack[i] = c.transform(entry, thisFirst: true);
        c = entry.transform(c, thisFirst: false);
      }
    }
    _lastTyping = null;
  }

  void _send(Edit edit) {
    _pending = ++_n;
    _inflight = edit;
    _transmit();
  }

  void _transmit() {
    _sent = true;
    _transport?.send(jsonEncode({'t': 'op', 'n': _pending, 'v': _rev, 'd': _inflight!.toJson()}));
  }

  void _flush() {
    final buffer = _buffer;
    if (_pending != 0 || buffer == null || !_synced) return;
    _buffer = null;
    _send(buffer);
  }

  Future<void> _open() async {
    _setStatus(DocStatus.connecting);
    await _restore();
    if (!_running) return;
    final DocTransport transport;
    try {
      transport = await _connect(clientId);
    } on Object catch (error) {
      if (!_running) return;
      _failure = error;
      _setStatus(DocStatus.offline);
      _scheduleRetry();
      return;
    }
    if (!_running) {
      await transport.close();
      return;
    }
    _transport = transport;
    _synced = false;
    _sent = false;
    _subscription = transport.messages.listen(
      _receive,
      onDone: () => _dropped(transport),
      onError: (Object _) {},
      cancelOnError: false,
    );
  }

  void _dropped(DocTransport transport) {
    if (!identical(transport, _transport)) return;
    _transport = null;
    _subscription = null;
    _synced = false;
    _peers.clear();
    (presence as _Presence).changed();
    if (!_running) return;
    final code = transport.closeCode;
    if (code == 4000 || code == 4001) {
      _running = false;
      _failure = DocClosed(code!, transport.closeReason ?? '');
      _setStatus(DocStatus.closed);
      return;
    }
    _setStatus(DocStatus.offline);
    _scheduleRetry();
  }

  void _scheduleRetry() {
    final delay = _backoff[math.min(_attempt, _backoff.length - 1)];
    _attempt++;
    _retry = Timer(Duration(seconds: delay), () {
      if (_running) unawaited(_open());
    });
  }

  void _setStatus(DocStatus status) {
    if (_disposed) return;
    _status = status;
    notifyListeners();
  }

  void _receive(String raw) {
    final Object? decoded;
    try {
      decoded = jsonDecode(raw);
    } on FormatException {
      return;
    }
    if (decoded is! Map<String, Object?>) return;
    switch (decoded['t']) {
      case 'hello':
        _hello(decoded);
      case 'doc':
        _whole(decoded);
      case 'ready':
        _ready();
      case 'op':
        _remote(decoded);
      case 'ack':
        _acknowledged(_int(decoded['n']), _int(decoded['v']));
      case 'nack':
        _refused(_int(decoded['n']), '${decoded['error'] ?? ''}');
      case 'eph':
        _presence(decoded);
      case 'join':
        final peer = _peer(decoded['peer']);
        if (peer != null) _peers[peer.sid] = peer;
        notifyListeners();
      case 'leave':
        _peers.remove(_int(decoded['sid']));
        notifyListeners();
        (presence as _Presence).changed();
      case 'saved':
        _savedVersion = math.max(_savedVersion, _int(decoded['v']));
        _saveError = null;
        notifyListeners();
      case 'error':
        _saveError = '${decoded['error'] ?? ''}';
        notifyListeners();
    }
  }

  void _hello(Map<String, Object?> hello) {
    _readOnly = hello['ro'] == true;
    _id = '${hello['id'] ?? ''}';
    _name = '${hello['name'] ?? ''}';
    _savedVersion = _int(hello['saved']);
    _saveError = hello['error'] is String ? hello['error']! as String : null;
    _joined = hello['epoch'] is String ? hello['epoch']! as String : null;
    _peers.clear();
    for (final raw in _list(hello['peers'])) {
      final peer = _peer(raw);
      if (peer != null) _peers[peer.sid] = peer;
    }
    _transport?.send(jsonEncode({
      't': 'sync',
      if (_doc != null && _epoch != null) ...{'epoch': _epoch, 'v': _rev},
    }));
  }

  /// The whole document: the first one, or one this client could not catch
  /// up with edit by edit. Its own edits the hub has not applied are rebased
  /// over what changed, found by comparing the two documents.
  void _whole(Map<String, Object?> frame) {
    final nodes = Edit.fromJson(frame['d']);
    final tree = nodes == null ? null : Tree.fromEdit(nodes);
    if (tree == null) return;
    if (_doc == null) {
      _doc = tree.copy();
      _changes.add(nodes!);
    } else {
      final applied = _pending != 0 && _int(frame['ack']) >= _pending;
      final base = _confirmed.copy();
      var mine = _buffer ?? Edit();
      if (_pending != 0) {
        if (applied) {
          base.apply(_inflight!);
        } else {
          mine = _inflight!.compose(mine);
        }
      }
      final theirs = diffTrees(base, tree);
      var rebased = theirs.transform(mine, thisFirst: true);
      final shown = mine.transform(theirs, thisFirst: false);
      _pending = 0;
      _inflight = null;
      final doc = tree.copy();
      if (doc.apply(rebased) == null) rebased = Edit();
      _buffer = rebased.isEmpty ? null : rebased;
      _doc = doc;
      _rebaseHistory(shown);
      _moved(shown, author: -1);
    }
    _confirmed = tree;
    _rev = _int(frame['v']);
    _ready();
  }

  void _ready() {
    _synced = true;
    _epoch = _joined;
    _attempt = 0;
    _failure = null;
    if (_pending != 0 && !_sent) _transmit();
    _flush();
    _selectionDirty = _selection != null;
    _flushPresence();
    _setStatus(DocStatus.online);
    (presence as _Presence).changed();
  }

  void _remote(Map<String, Object?> frame) {
    var edit = Edit.fromJson(frame['d']);
    if (edit == null || _doc == null) return;
    _confirmed.apply(edit);
    final inflight = _inflight;
    if (inflight != null) {
      _inflight = edit.transform(inflight, thisFirst: true);
      edit = inflight.transform(edit, thisFirst: false);
    }
    final buffer = _buffer;
    if (buffer != null) {
      _buffer = edit.transform(buffer, thisFirst: true);
      edit = buffer.transform(edit, thisFirst: false);
    }
    _rev = _int(frame['v']);
    _rebaseHistory(edit);
    _show(edit, author: _int(frame['sid']));
    notifyListeners();
    (presence as _Presence).changed();
  }

  void _acknowledged(int n, int version) {
    if (n != _pending) return;
    _confirmed.apply(_inflight!);
    _rev = version;
    _ackVersion = math.max(_ackVersion, version);
    _pending = 0;
    _inflight = null;
    _flush();
    _draftChanged();
    notifyListeners();
  }

  /// Rolls back the edit in flight, keeping the ones made after it.
  void _refused(int n, String reason) {
    if (n != _pending) return;
    final undo = _confirmed.copy().apply(_inflight!) ?? Edit();
    var shown = undo;
    final buffer = _buffer;
    if (buffer != null) {
      _buffer = undo.transform(buffer, thisFirst: true);
      shown = buffer.transform(undo, thisFirst: false);
    }
    _pending = 0;
    _inflight = null;
    _rebaseHistory(shown);
    _show(shown, author: -1);
    _rejections.add(reason);
    _flush();
    notifyListeners();
  }

  void _presence(Map<String, Object?> frame) {
    final peer = _peers[_int(frame['sid'])];
    final data = frame['d'];
    if (peer == null || data is! Map<String, Object?> || !data.containsKey('s')) return;
    peer._selection = DocSelection.fromJson(data['s']);
    (presence as _Presence).changed();
  }

  static DocPeer? _peer(Object? raw) {
    if (raw is! Map<String, Object?>) return null;
    return DocPeer._(_int(raw['sid']), '${raw['id'] ?? ''}', '${raw['name'] ?? ''}', raw['ro'] == true);
  }

  static List<Object?> _list(Object? raw) => raw is List<Object?> ? raw : const [];

  static int _int(Object? raw) => raw is num ? raw.toInt() : 0;
}

class _Presence extends ChangeNotifier {
  void changed() => notifyListeners();
}

final _random = math.Random.secure();
const _alphabet = '0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz';

/// 16 random characters: 95 bits, enough for ids nobody coordinates.
String randomId() => String.fromCharCodes([
  for (var i = 0; i < 16; i++) _alphabet.codeUnitAt(_random.nextInt(_alphabet.length)),
]);
