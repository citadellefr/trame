import 'dart:collection';

import 'dart:math';

import 'package:flutter/foundation.dart';

import 'delta.dart';
import 'diff.dart';
import 'flow.dart';
import 'grid.dart';

/// One element of a [Tree]: a slide, a shape, the body of a text file, a
/// sheet.
@immutable
class Node {
  const Node({
    required this.id,
    required this.type,
    this.parent = '',
    required this.key,
    this.attributes = const {},
    this._text,
    this.grid,
  }) : _flow = null;

  const Node._({
    required this.id,
    required this.type,
    required this.parent,
    required this.key,
    required this.attributes,
    required this._text,
    required this._flow,
    required this.grid,
  });

  final String id;
  final String type;

  /// The id of the node this one is under, empty at the root.
  final String parent;

  /// Where the node stands among its siblings, see [keyBetween].
  final String key;

  /// JSON values, never null.
  final Map<String, Object?> attributes;

  final Delta? _text;

  /// The flow as a tree keeps it once edited: by paragraph.
  final Flow? _flow;

  /// The node's flow of text, if it holds one.
  Delta? get text => _flow?.delta ?? _text;

  /// The length of [text], without making it.
  int? get textLength => _flow?.length ?? _text?.length;

  /// The node's cells, if it holds some.
  final Grid? grid;

  Node _with({String? key, Map<String, Object?>? attributes, Delta? text, Flow? flow, Grid? grid}) => Node._(
    id: id,
    type: type,
    parent: parent,
    key: key ?? this.key,
    attributes: attributes ?? this.attributes,
    text: flow != null ? null : (text ?? _text),
    flow: flow ?? (text != null ? null : _flow),
    grid: grid ?? this.grid,
  );

  @override
  String toString() => '${Change.create(this).toJson()}';
}

enum ChangeKind {
  create('new'),
  delete('del'),
  set('set'),
  text('txt'),
  cells('cel'),
  insert('ins'),
  remove('rem');

  const ChangeKind(this.code);

  final String code;

  bool get isGrid => this == cells || this == insert || this == remove;
}

/// One step of an [Edit]: a node created, deleted, given attributes or a
/// new key, its text changed, cells set, or rows or columns inserted or
/// removed in its grid.
@immutable
class Change {
  Change.create(Node node) : this._create(node, node.grid?.cells.toList());

  /// Creates [node] with its [cells] in the order given.
  Change._create(Node node, this.cells)
    : kind = ChangeKind.create,
      id = node.id,
      type = node.type,
      parent = node.parent,
      key = node.key,
      attributes = node.attributes,
      text = node.text,
      dim = '',
      at = 0,
      n = 0;

  const Change.delete(this.id)
    : kind = ChangeKind.delete,
      type = '',
      parent = '',
      key = '',
      attributes = const {},
      text = null,
      cells = null,
      dim = '',
      at = 0,
      n = 0;

  /// A null value removes the attribute; an empty [key] leaves the key.
  const Change.set(this.id, {this.key = '', this.attributes = const {}})
    : kind = ChangeKind.set,
      type = '',
      parent = '',
      text = null,
      cells = null,
      dim = '',
      at = 0,
      n = 0;

  const Change.text(this.id, Delta this.text)
    : kind = ChangeKind.text,
      type = '',
      parent = '',
      key = '',
      attributes = const {},
      cells = null,
      dim = '',
      at = 0,
      n = 0;

  /// Sets fields of [cells]; a null value removes the field.
  const Change.cells(this.id, List<Cell> this.cells)
    : kind = ChangeKind.cells,
      type = '',
      parent = '',
      key = '',
      attributes = const {},
      text = null,
      dim = '',
      at = 0,
      n = 0;

  /// Inserts [n] rows ([dimRows]) or columns ([dimCols]) at [at].
  const Change.insert(this.id, this.dim, this.at, this.n)
    : kind = ChangeKind.insert,
      type = '',
      parent = '',
      key = '',
      attributes = const {},
      text = null,
      cells = null;

  /// Removes [n] rows or columns from [at].
  const Change.remove(this.id, this.dim, this.at, this.n)
    : kind = ChangeKind.remove,
      type = '',
      parent = '',
      key = '',
      attributes = const {},
      text = null,
      cells = null;

  final ChangeKind kind;
  final String id;
  final String type;
  final String parent;
  final String key;
  final Map<String, Object?> attributes;
  final Delta? text;
  final List<Cell>? cells;
  final String dim;
  final int at;
  final int n;

  Node get node => Node(
    id: id,
    type: type,
    parent: parent,
    key: key,
    attributes: attributes,
    text: text,
    grid: cells == null ? null : Grid(cells!),
  );

  static Change? fromJson(Object? json) {
    if (json is! Map<String, Object?>) return null;
    final id = json['id'], type = json['t'] ?? '', parent = json['p'] ?? '', key = json['k'] ?? '';
    final attributes = json['a'] ?? const <String, Object?>{};
    if (id is! String || !_isName(id) || type is! String || parent is! String || key is! String) return null;
    if (key.isNotEmpty && !_isName(key) || attributes is! Map<String, Object?>) return null;
    if (attributes.keys.any((k) => k.isEmpty)) return null;
    final text = json.containsKey('x') ? Delta.fromJson(json['x']) : null;
    if (json.containsKey('x') && text == null) return null;
    final op = json['o'];
    final cells = json.containsKey('c') ? _cells(json['c'], set: op == 'cel') : null;
    if (json.containsKey('c') && cells == null) return null;
    final dim = json['dim'] ?? '', at = json['at'] ?? 0, n = json['n'] ?? 0;
    if (dim is! String || at is! int || n is! int) return null;
    final bare = type.isEmpty && parent.isEmpty;
    final noGrid = cells == null && dim.isEmpty && at == 0 && n == 0;
    final plain = bare && key.isEmpty && attributes.isEmpty && text == null;
    switch (op) {
      case 'new':
        if (!_isName(type) || !_isName(key) || parent.isNotEmpty && !_isName(parent)) return null;
        if (attributes.values.contains(null) || text != null && !_isFlow(text)) return null;
        if (dim.isNotEmpty || at != 0 || n != 0 || cells != null && text != null) return null;
        return Change._create(
          Node(
            id: id,
            type: type,
            parent: parent,
            key: key,
            attributes: attributes,
            text: text,
            grid: cells == null ? null : Grid(cells),
          ),
          cells,
        );
      case 'del' when plain && noGrid:
        return Change.delete(id);
      case 'set' when bare && text == null && (key.isNotEmpty || attributes.isNotEmpty) && noGrid:
        return Change.set(id, key: key, attributes: attributes);
      case 'txt' when bare && key.isEmpty && attributes.isEmpty && noGrid:
        return Change.text(id, text ?? Delta());
      case 'cel' when plain && cells != null && cells.isNotEmpty && dim.isEmpty && at == 0 && n == 0:
        if (cells.any((c) => c.fields.isEmpty)) return null;
        return Change.cells(id, cells);
      case 'ins' || 'rem' when plain && cells == null && (dim == dimRows || dim == dimCols):
        final limit = dim == dimRows ? maxRows : maxCols;
        if (at < 1 || at > limit || n < 1 || n > limit) return null;
        return op == 'ins' ? Change.insert(id, dim, at, n) : Change.remove(id, dim, at, n);
    }
    return null;
  }

  static List<Cell>? _cells(Object? json, {required bool set}) {
    if (json is! List<Object?>) return null;
    final cells = <Cell>[];
    final seen = <(int, int)>{};
    for (final raw in json) {
      final cell = Cell.fromJson(raw, set: set);
      if (cell == null || !seen.add((cell.row, cell.col))) return null;
      cells.add(cell);
    }
    return cells;
  }

  Map<String, Object?> toJson() => {
    'o': kind.code,
    'id': id,
    if (type.isNotEmpty) 't': type,
    if (parent.isNotEmpty) 'p': parent,
    if (key.isNotEmpty) 'k': key,
    if (attributes.isNotEmpty) 'a': attributes,
    if (text != null && !text!.isEmpty) 'x': text!.toJson(),
    if (cells != null) 'c': [for (final c in cells!) c.toJson()],
    if (dim.isNotEmpty) 'dim': dim,
    if (at != 0) 'at': at,
    if (n != 0) 'n': n,
  };

  @override
  bool operator ==(Object other) =>
      other is Change &&
      other.kind == kind &&
      other.id == id &&
      other.type == type &&
      other.parent == parent &&
      other.key == key &&
      jsonEquals(other.attributes, attributes) &&
      other.text == text &&
      (other.cells == null) == (cells == null) &&
      (cells == null || listEquals(other.cells, cells)) &&
      other.dim == dim &&
      other.at == at &&
      other.n == n;

  @override
  int get hashCode => Object.hash(kind, id, key, text, dim, at, n);

  @override
  String toString() => '${toJson()}';
}

/// What one person does at once: changes that apply together or not at all.
@immutable
class Edit {
  Edit([Iterable<Change> changes = const []]) : changes = List.unmodifiable(changes);

  final List<Change> changes;

  bool get isEmpty => changes.isEmpty;

  /// How much the edit can grow a tree at most.
  int get growth => changes.fold(0, (n, c) {
    final text = c.text?.change ?? 0;
    return n + switch (c.kind) {
      ChangeKind.create => 1 + (text > 0 ? text : 0) + (c.cells?.length ?? 0),
      ChangeKind.text => text > 0 ? text : 0,
      ChangeKind.cells => c.cells!.length,
      _ => 0,
    };
  });

  static Edit? fromJson(Object? json) {
    if (json is! List<Object?>) return null;
    final changes = <Change>[];
    for (final raw in json) {
      final change = Change.fromJson(raw);
      if (change == null) return null;
      changes.add(change);
    }
    return Edit(changes);
  }

  List<Object> toJson() => [for (final c in changes) c.toJson()];

  /// This edit, then [other]; text changed twice in a row by one change
  /// each is changed once.
  Edit compose(Edit other) {
    if (isEmpty) return other;
    if (other.isEmpty) return this;
    final last = changes.last, next = other.changes.first;
    if (last.kind == ChangeKind.text && next.kind == ChangeKind.text && last.id == next.id) {
      return Edit([
        ...changes.take(changes.length - 1),
        Change.text(last.id, last.text!.compose(next.text!)),
        ...other.changes.skip(1),
      ]);
    }
    return Edit([...changes, ...other.changes]);
  }

  /// Rebases [other], made concurrently with this edit, to apply after it.
  /// Text changed by both is transformed as [Delta.transform] does; when
  /// both set the same attribute or key, the one ordered second wins, as
  /// [thisFirst] says.
  Edit transform(Edit other, {required bool thisFirst}) {
    final mine = <Change?>[...changes];
    final out = <Change>[];
    for (Change? c in other.changes) {
      for (var i = 0; i < mine.length && c != null; i++) {
        final a = mine[i];
        if (a == null) continue;
        final (x, y) = _transform(a, c, thisFirst: thisFirst);
        mine[i] = x;
        c = y;
      }
      if (c != null) out.add(c);
    }
    return Edit(out);
  }

  /// This edit with the ids of [names] replaced, parents included.
  Edit renamed(Map<String, String> names) => Edit([
    for (final c in changes)
      if (!names.containsKey(c.id) && !names.containsKey(c.parent))
        c
      else
        switch (c.kind) {
          ChangeKind.create => Change._create(
            Node(
              id: names[c.id] ?? c.id,
              type: c.type,
              parent: names[c.parent] ?? c.parent,
              key: c.key,
              attributes: c.attributes,
              text: c.text,
              grid: c.cells == null ? null : Grid(c.cells!),
            ),
            c.cells,
          ),
          ChangeKind.delete => Change.delete(names[c.id]!),
          ChangeKind.set => Change.set(names[c.id]!, key: c.key, attributes: c.attributes),
          ChangeKind.text => Change.text(names[c.id]!, c.text!),
          ChangeKind.cells => Change.cells(names[c.id]!, c.cells!),
          ChangeKind.insert => Change.insert(names[c.id]!, c.dim, c.at, c.n),
          ChangeKind.remove => Change.remove(names[c.id]!, c.dim, c.at, c.n),
        },
  ]);

  @override
  bool operator ==(Object other) => other is Edit && listEquals(other.changes, changes);

  @override
  int get hashCode => Object.hashAll(changes);

  @override
  String toString() => '$changes';
}

/// [a] and [b] rebased over each other; null for a change left with
/// nothing to do.
(Change?, Change?) _transform(Change a, Change b, {required bool thisFirst}) {
  if (a.id == b.id && a.kind.isGrid && b.kind.isGrid) return _transformGrid(a, b, thisFirst: thisFirst);
  if (a.id != b.id || a.kind != b.kind) return (a, b);
  switch (a.kind) {
    case ChangeKind.text:
      final x = b.text!.transform(a.text!, thisFirst: !thisFirst).chop();
      final y = a.text!.transform(b.text!, thisFirst: thisFirst).chop();
      return (x.isEmpty ? null : Change.text(a.id, x), y.isEmpty ? null : Change.text(b.id, y));
    case ChangeKind.set:
      return thisFirst ? (_overridden(a, b), b) : (a, _overridden(b, a));
    default:
      return (a, b);
  }
}

/// [c] without what [later] sets.
Change? _overridden(Change c, Change later) {
  final key = later.key.isNotEmpty ? '' : c.key;
  final attributes = {
    for (final e in c.attributes.entries)
      if (!later.attributes.containsKey(e.key)) e.key: e.value,
  };
  if (key.isEmpty && attributes.isEmpty) return null;
  return Change.set(c.id, key: key, attributes: attributes);
}

/// [a] and [b], two changes to the same grid, rebased over each other.
(Change?, Change?) _transformGrid(Change a, Change b, {required bool thisFirst}) {
  Change? x = a, y = b;
  if (a.kind == ChangeKind.cells && b.kind == ChangeKind.cells) {
    if (thisFirst) {
      x = _cellsChange(a.id, _overriddenCells(a.cells!, b.cells!));
    } else {
      y = _cellsChange(b.id, _overriddenCells(b.cells!, a.cells!));
    }
  } else if (a.kind == ChangeKind.cells) {
    x = _cellsChange(a.id, _shiftCells(a.cells!, b));
  } else if (b.kind == ChangeKind.cells) {
    y = _cellsChange(b.id, _shiftCells(b.cells!, a));
  } else if (a.dim != b.dim) {
  } else if (a.kind == ChangeKind.insert && b.kind == ChangeKind.insert) {
    if (a.at < b.at || a.at == b.at && thisFirst) {
      y = Change.insert(b.id, b.dim, b.at + a.n, b.n);
    } else {
      x = Change.insert(a.id, a.dim, a.at + b.n, a.n);
    }
  } else if (a.kind == ChangeKind.remove && b.kind == ChangeKind.remove) {
    x = _removedOver(a, b);
    y = _removedOver(b, a);
  } else if (a.kind == ChangeKind.insert) {
    (y, x) = _removalOverInsert(b, a);
  } else {
    (x, y) = _removalOverInsert(a, b);
  }
  return (x, y);
}

Change? _cellsChange(String id, List<Cell> cells) => cells.isEmpty ? null : Change.cells(id, cells);

/// [cells] without the fields [later] sets.
List<Cell> _overriddenCells(List<Cell> cells, List<Cell> later) {
  final sets = {for (final c in later) (c.row, c.col): c.fields};
  final out = <Cell>[];
  for (final c in cells) {
    final set = sets[(c.row, c.col)];
    final fields = set == null ? c.fields : {
      for (final e in c.fields.entries)
        if (!set.containsKey(e.key)) e.key: e.value,
    };
    if (fields.isNotEmpty) out.add(Cell(c.row, c.col, fields));
  }
  return out;
}

/// [cells] where the rows or columns inserted or removed by [s] put them,
/// without those removed.
List<Cell> _shiftCells(List<Cell> cells, Change s) {
  final n = s.kind == ChangeKind.remove ? -s.n : s.n;
  final out = <Cell>[];
  for (final c in cells) {
    final rows = s.dim == dimRows;
    final i = movedIndex(rows ? c.row : c.col, s.at, n, rows ? maxRows : maxCols);
    if (i != null) out.add(rows ? Cell(i, c.col, c.fields) : Cell(c.row, i, c.fields));
  }
  return out;
}

/// The removal [r] once [other] is removed too.
Change? _removedOver(Change r, Change other) {
  final end = r.at + r.n, otherEnd = other.at + other.n;
  final before = max(0, min(otherEnd, r.at) - other.at).toInt();
  final overlap = max(0, min(end, otherEnd) - max(r.at, other.at)).toInt();
  return r.n - overlap <= 0 ? null : Change.remove(r.id, r.dim, r.at - before, r.n - overlap);
}

/// A removal and an insertion rebased over each other: rows inserted
/// inside the removed ones are removed with them.
(Change?, Change?) _removalOverInsert(Change rem, Change ins) {
  if (ins.at <= rem.at) return (Change.remove(rem.id, rem.dim, rem.at + ins.n, rem.n), ins);
  if (ins.at >= rem.at + rem.n) return (rem, Change.insert(ins.id, ins.dim, ins.at - rem.n, ins.n));
  return (Change.remove(rem.id, rem.dim, rem.at, rem.n + ins.n), null);
}

/// A document made of nodes, as the Go package `ot` keeps it: each node
/// sits under its parent, ordered among its siblings by key then id, and
/// may hold a flow of text or a grid of cells. A change to a node that no longer exists does
/// nothing.
class Tree {
  Tree();

  /// The tree [nodes] create, parents first; null unless they only create.
  static Tree? fromEdit(Edit nodes) {
    if (nodes.changes.any((c) => c.kind != ChangeKind.create)) return null;
    final tree = Tree();
    return tree.apply(nodes) == null ? null : tree;
  }

  final _nodes = HashMap<String, Node>();
  final _kids = HashMap<String, List<String>>();
  final _sorted = HashMap<String, List<Node>>();
  var _length = 0;

  /// The nodes and the length of their text.
  int get length => _length;

  Iterable<Node> get nodes => _nodes.values;

  Node? operator [](String id) => _nodes[id];

  /// The nodes under [parent], in order; '' is the root.
  List<Node> children(String parent) => _sorted[parent] ??= List.unmodifiable(
    (_kids[parent] ?? const <String>[]).map((id) => _nodes[id]!).toList()..sort(_compare),
  );

  /// Whether [id] is [ancestor] or under it.
  bool isUnder(String id, String ancestor) {
    for (String? at = id; at != null && at.isNotEmpty; at = _nodes[at]?.parent) {
      if (at == ancestor) return true;
    }
    return false;
  }

  Tree copy() {
    final tree = Tree();
    tree._nodes.addAll(_nodes);
    for (final e in _kids.entries) {
      tree._kids[e.key] = [...e.value];
    }
    tree._length = _length;
    return tree;
  }

  /// The whole tree as the changes that create it, parents first.
  Edit toEdit() => Edit(_subtree(''));

  List<Change> _subtree(String parent, [List<Change>? out]) {
    out ??= [];
    for (final node in children(parent)) {
      out.add(Change.create(node));
      _subtree(node.id, out);
    }
    return out;
  }

  /// Applies [edit] and returns the edit that undoes it, or leaves the tree
  /// as it was and returns null when [edit] does not apply to it.
  Edit? apply(Edit edit) {
    final undo = <Change>[];
    final saved = <(String, Node?)>[];
    for (final c in edit.changes) {
      if (!_apply(c, undo, saved)) {
        for (final (id, node) in saved.reversed) {
          _put(id, node);
        }
        return null;
      }
    }
    return Edit(undo.reversed);
  }

  bool _apply(Change c, List<Change> undo, List<(String, Node?)> saved) {
    final node = _nodes[c.id];
    if (c.kind == ChangeKind.create) {
      if (node != null) return false;
      if (c.parent.isNotEmpty && !_nodes.containsKey(c.parent)) return true;
      saved.add((c.id, null));
      _put(c.id, c.node);
      undo.add(Change.delete(c.id));
      return true;
    }
    if (node == null) return true;
    switch (c.kind) {
      case ChangeKind.delete:
        final gone = _subtree(node.id, [Change.create(node)]);
        undo.addAll(gone.reversed);
        for (final g in gone.reversed) {
          saved.add((g.id, _nodes[g.id]));
          _put(g.id, null);
        }
      case ChangeKind.set:
        final attributes = {...node.attributes};
        for (final e in c.attributes.entries) {
          if (e.value == null) {
            attributes.remove(e.key);
          } else {
            attributes[e.key] = e.value;
          }
        }
        undo.add(Change.set(
          c.id,
          key: c.key.isEmpty ? '' : node.key,
          attributes: {for (final k in c.attributes.keys) k: node.attributes[k]},
        ));
        saved.add((c.id, node));
        _put(c.id, node._with(key: c.key.isEmpty ? null : c.key, attributes: attributes));
      case ChangeKind.text:
        final delta = c.text!;
        final flow = node._flow ?? (node._text == null ? null : Flow.of(node._text));
        if (flow != null) {
          final (result, inverse) = flow.apply(delta) ?? (null, null);
          if (result == null) return false;
          undo.add(Change.text(c.id, inverse!));
          saved.add((c.id, node));
          _put(c.id, node._with(flow: result));
          return true;
        }
        final text = node.text;
        if (text == null || delta.baseLength > text.length || _deletesLast(delta, text.length)) return false;
        final result = text.compose(delta);
        if (!_isFlow(result)) return false;
        undo.add(Change.text(c.id, delta.invert(text)));
        saved.add((c.id, node));
        _put(c.id, node._with(text: result));
      case ChangeKind.cells || ChangeKind.insert || ChangeKind.remove:
        final grid = node.grid;
        if (grid == null) return false;
        saved.add((c.id, node));
        _put(c.id, node._with(grid: _applyGrid(grid, c, undo)));
      case ChangeKind.create:
    }
    return true;
  }

  static Grid _applyGrid(Grid grid, Change c, List<Change> undo) {
    switch (c.kind) {
      case ChangeKind.cells:
        final old = <Cell>[];
        for (final cell in c.cells!) {
          final f = grid.cell(cell.row, cell.col) ?? const {};
          if (cell.fields.entries.every((e) => e.value == null && f[e.key] == null)) continue;
          old.add(Cell(cell.row, cell.col, {for (final k in cell.fields.keys) k: f[k]}));
        }
        if (old.isNotEmpty) undo.add(Change.cells(c.id, old));
        return grid.set(c.cells!);
      case ChangeKind.insert:
        undo.add(Change.remove(c.id, c.dim, c.at, c.n));
        return grid.shift(c.dim, c.at, c.n);
      default:
        final gone = grid.cells.where((cell) {
          final i = c.dim == dimRows ? cell.row : cell.col;
          return i >= c.at && i < c.at + c.n;
        }).toList();
        if (gone.isNotEmpty) undo.add(Change.cells(c.id, gone));
        undo.add(Change.insert(c.id, c.dim, c.at, c.n));
        return grid.shift(c.dim, c.at, -c.n);
    }
  }

  void _put(String id, Node? node) {
    final old = _nodes[id];
    if (old != null) {
      _length -= _size(old);
      _sorted.remove(old.parent);
      if (node == null) {
        final kids = _kids[old.parent]!..remove(id);
        if (kids.isEmpty) _kids.remove(old.parent);
      }
    } else if (node != null) {
      (_kids[node.parent] ??= []).add(id);
    }
    if (node == null) {
      _nodes.remove(id);
      return;
    }
    _nodes[id] = node;
    _sorted.remove(node.parent);
    _length += _size(node);
  }

  static int _size(Node node) => 1 + (node.textLength ?? 0) + (node.grid?.length ?? 0);

  static int _compare(Node a, Node b) {
    final byKey = a.key.compareTo(b.key);
    return byKey != 0 ? byKey : a.id.compareTo(b.id);
  }
}

/// The edit that turns [from] into [to]: nodes deleted and created, keys
/// and attributes set, text diffed by paragraph then by character.
Edit diffTrees(Tree from, Tree to) {
  final out = <Change>[];
  for (final node in from.nodes) {
    if (to[node.id] == null && (node.parent.isEmpty || to[node.parent] != null)) {
      out.add(Change.delete(node.id));
    }
  }
  for (final c in to.toEdit().changes) {
    final old = from[c.id];
    if (old == null) {
      out.add(c);
      continue;
    }
    final attributes = <String, Object?>{
      for (final e in c.attributes.entries)
        if (!jsonEquals(old.attributes[e.key], e.value)) e.key: e.value,
      for (final k in old.attributes.keys)
        if (!c.attributes.containsKey(k)) k: null,
    };
    if (old.key != c.key || attributes.isNotEmpty) {
      out.add(Change.set(c.id, key: old.key == c.key ? '' : c.key, attributes: attributes));
    }
    final a = old.text, b = c.text;
    if (a != null && b != null) {
      final d = diff(a.text, b.text);
      if (!d.isEmpty) out.add(Change.text(c.id, d));
    }
    final before = old.grid, after = c.cells;
    if (before != null && after != null) {
      final cells = _diffCells(before, Grid(after));
      if (cells.isNotEmpty) out.add(Change.cells(c.id, cells));
    }
  }
  return Edit(out);
}

List<Cell> _diffCells(Grid from, Grid to) {
  final out = <Cell>[];
  for (final cell in to.cells) {
    final old = from.cell(cell.row, cell.col) ?? const {};
    final fields = {
      for (final e in cell.fields.entries)
        if (!jsonEquals(old[e.key], e.value)) e.key: e.value,
      for (final k in old.keys)
        if (!cell.fields.containsKey(k)) k: null,
    };
    if (fields.isNotEmpty) out.add(Cell(cell.row, cell.col, fields));
  }
  for (final cell in from.cells) {
    if (to.cell(cell.row, cell.col) == null) out.add(Cell(cell.row, cell.col, {for (final k in cell.fields.keys) k: null}));
  }
  return out;
}

/// Whether [delta] deletes the last unit of a flow of [length] units, its
/// final mark, which no edit may.
bool _deletesLast(Delta delta, int length) {
  var at = 0;
  for (final op in delta.ops) {
    if (op.isDelete && at + op.delete == length) return true;
    at += op.delete + op.retain;
  }
  return false;
}

bool _isFlow(Delta d) => !d.isEmpty && d.ops.every((op) => op.isInsert) && d.ops.last.insert!.endsWith('\n');

/// An id, a type or a key: short, printable ASCII, ordered alike in Go and
/// Dart.
bool _isName(String s) => s.isNotEmpty && s.length <= 64 && s.codeUnits.every((c) => c >= 0x21 && c <= 0x7E);


const _digits = '0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz';

/// A key after [a] and before [b]; an empty [a] is the start, an empty [b]
/// the end. Keys are base 62 digits compared unit by unit that never end
/// with the smallest digit, so that there is always room before them.
String keyBetween(String a, String b) {
  if (b.isNotEmpty && a.compareTo(b) >= 0) throw ArgumentError('keyBetween($a, $b)');
  return _midpoint(a, b);
}

String _midpoint(String a, String b) {
  if (b.isNotEmpty) {
    var n = 0;
    while (n < b.length && _digitAt(a, n) == b[n]) {
      n++;
    }
    if (n > 0) return b.substring(0, n) + _midpoint(a.length > n ? a.substring(n) : '', b.substring(n));
  }
  final lo = _digits.indexOf(_digitAt(a, 0));
  final hi = b.isEmpty ? _digits.length : _digits.indexOf(b[0]);
  if (hi - lo > 1) return _digits[(lo + hi + 1) ~/ 2];
  if (b.length > 1) return b[0];
  return _digits[lo] + _midpoint(a.length > 1 ? a.substring(1) : '', '');
}

String _digitAt(String s, int i) => i < s.length ? s[i] : _digits[0];
