import 'dart:collection';
import 'dart:math' as math;

import 'package:flutter/foundation.dart';

/// Formatting attributes. In a retain, an empty value removes the attribute.
typedef Attributes = Map<String, String>;

/// One step of a [Delta]: it inserts, deletes or retains.
@immutable
class Op {
  const Op.insert(String this.insert, [this.attributes]) : delete = 0, retain = 0;

  const Op.delete(this.delete) : insert = null, retain = 0, attributes = null;

  const Op.retain(this.retain, [this.attributes]) : insert = null, delete = 0;

  final String? insert;
  final int delete;
  final int retain;
  final Attributes? attributes;

  bool get isInsert => insert != null;

  bool get isDelete => delete > 0;

  bool get isRetain => retain > 0;

  /// In UTF-16 code units, as Dart strings count.
  int get length => insert?.length ?? (delete > 0 ? delete : retain);

  Object toJson() => {
    if (insert != null) 'i': insert,
    if (delete > 0) 'd': delete,
    if (retain > 0) 'r': retain,
    if (attributes != null) 'a': attributes,
  };

  static Op? fromJson(Object? json) {
    if (json is! Map<String, Object?>) return null;
    final a = json['a'];
    Attributes? attributes;
    if (a is Map<String, Object?>) {
      attributes = {
        for (final entry in a.entries)
          if (entry.value is String) entry.key: entry.value! as String,
      };
      if (attributes.length != a.length) return null;
    }
    return switch (json) {
      {'i': final String s} when s.isNotEmpty => Op.insert(s, attributes),
      {'d': final int n} when n > 0 => Op.delete(n),
      {'r': final int n} when n > 0 => Op.retain(n, attributes),
      _ => null,
    };
  }

  @override
  bool operator ==(Object other) =>
      other is Op &&
      other.insert == insert &&
      other.delete == delete &&
      other.retain == retain &&
      mapEquals(other.attributes, attributes);

  @override
  int get hashCode => Object.hash(insert, delete, retain, attributes?.length);

  @override
  String toString() => '${toJson()}';
}

/// A change to a flow, or a whole flow when it only inserts.
///
/// Text is a flow of characters and paragraph marks, where a mark is a "\n"
/// whose attributes are those of the paragraph it ends; every flow ends with
/// one. The algorithms are those of the Go package `ot`, checked against its
/// vectors.
class Delta {
  Delta([Iterable<Op> ops = const []]) {
    ops.forEach(push);
  }

  final _ops = <Op>[];

  List<Op> get ops => UnmodifiableListView(_ops);

  bool get isEmpty => _ops.isEmpty;

  /// The length of the flow, when the delta only inserts.
  int get length => _ops.fold(0, (n, op) => n + op.length);

  /// The length of the flow the delta applies to, the retain implied at its
  /// end left out.
  int get baseLength => _ops.fold(0, (n, op) => n + op.delete + op.retain);

  /// How much the delta grows the flow.
  int get change => _ops.fold(0, (n, op) => n + (op.insert?.length ?? 0) - op.delete);

  /// The text of the flow, marks included.
  String get text => _ops.map((op) => op.insert ?? '').join();

  static Delta? fromJson(Object? json) {
    if (json is! List<Object?>) return null;
    final delta = Delta();
    for (final raw in json) {
      final op = Op.fromJson(raw);
      if (op == null) return null;
      delta.push(op);
    }
    return delta;
  }

  List<Object> toJson() => [for (final op in _ops) op.toJson()];

  /// Appends an op, merging it with the last one when they combine, and
  /// keeping inserts before deletes: equal deltas are equal op for op.
  void push(Op op) {
    if (op.length == 0) return;
    if (op.attributes != null && op.attributes!.isEmpty) {
      op = op.isInsert ? Op.insert(op.insert!) : Op.retain(op.retain);
    }
    var index = _ops.length;
    if (index > 0) {
      var last = _ops[index - 1];
      if (op.isDelete && last.isDelete) {
        _ops[index - 1] = Op.delete(last.delete + op.delete);
        return;
      }
      if (last.isDelete && op.isInsert) {
        index--;
        if (index == 0) {
          _ops.insert(0, op);
          return;
        }
        last = _ops[index - 1];
      }
      if (mapEquals(op.attributes, last.attributes)) {
        if (op.isInsert && last.isInsert) {
          _ops[index - 1] = Op.insert(last.insert! + op.insert!, op.attributes);
          return;
        }
        if (op.isRetain && last.isRetain) {
          _ops[index - 1] = Op.retain(last.retain + op.retain, op.attributes);
          return;
        }
      }
    }
    _ops.insert(index, op);
  }

  void insert(String text, [Attributes? attributes]) => push(Op.insert(text, attributes));

  void delete(int length) => push(Op.delete(length));

  void retain(int length, [Attributes? attributes]) => push(Op.retain(length, attributes));

  /// Drops a final retain that changes nothing.
  Delta chop() {
    while (_ops.isNotEmpty && _ops.last.isRetain && _ops.last.attributes == null) {
      _ops.removeLast();
    }
    return this;
  }

  /// This delta, then [other].
  Delta compose(Delta other) {
    final x = _Iterator(_ops), y = _Iterator(other._ops);
    final out = Delta();
    while (x.hasNext || y.hasNext) {
      if (y.peekType == _Kind.insert) {
        out.push(y.next());
        continue;
      }
      if (x.peekType == _Kind.delete) {
        out.push(x.next());
        continue;
      }
      final n = math.min(x.peekLength, y.peekLength);
      final a = x.next(n), b = y.next(n);
      if (b.isRetain) {
        final attributes = _composeAttributes(a.attributes, b.attributes, keepRemovals: a.isRetain);
        out.push(a.isRetain ? Op.retain(a.retain, attributes) : Op.insert(a.insert!, attributes));
      } else if (b.isDelete && a.isRetain) {
        out.push(b);
      }
    }
    return out.chop();
  }

  /// Rebases [other], made concurrently with this delta, to apply after it.
  /// When both insert at the same place, [thisFirst] says whose text comes
  /// first; when both set the same attribute, the one ordered second wins.
  Delta transform(Delta other, {required bool thisFirst}) {
    final x = _Iterator(_ops), y = _Iterator(other._ops);
    final out = Delta();
    while (x.hasNext || y.hasNext) {
      if (x.peekType == _Kind.insert && (thisFirst || y.peekType != _Kind.insert)) {
        out.retain(x.next().length);
        continue;
      }
      if (y.peekType == _Kind.insert) {
        out.push(y.next());
        continue;
      }
      final n = math.min(x.peekLength, y.peekLength);
      final a = x.next(n), b = y.next(n);
      if (a.isDelete) continue;
      if (b.isDelete) {
        out.push(b);
      } else {
        out.retain(n, _transformAttributes(a.attributes, b.attributes, thisFirst: thisFirst));
      }
    }
    return out.chop();
  }

  /// Moves an offset over this delta. At an insert made at the offset,
  /// [thisFirst] puts the offset after the inserted text.
  int transformPosition(int position, {required bool thisFirst}) {
    var offset = 0;
    for (final op in _ops) {
      if (offset > position) break;
      final n = op.length;
      if (op.isDelete) {
        position -= math.min(n, position - offset);
        continue;
      }
      if (op.isInsert && (offset < position || thisFirst)) position += n;
      offset += n;
    }
    return position;
  }

  /// The delta that undoes this one, which applies to [base].
  Delta invert(Delta base) {
    final out = Delta();
    final it = _Iterator(base._ops);
    for (final op in _ops) {
      if (op.isInsert) {
        out.delete(op.length);
      } else if (op.isRetain && op.attributes == null) {
        out.retain(op.retain);
        it.skip(op.retain);
      } else {
        var left = op.length;
        while (left > 0) {
          final original = it.next(left);
          left -= original.length;
          if (op.isDelete) {
            out.push(original);
          } else {
            out.retain(original.length, _invertAttributes(op.attributes!, original.attributes));
          }
        }
      }
    }
    return out.chop();
  }

  @override
  bool operator ==(Object other) => other is Delta && listEquals(other._ops, _ops);

  @override
  int get hashCode => Object.hashAll(_ops);

  @override
  String toString() => '$_ops';
}

Attributes? _composeAttributes(Attributes? a, Attributes? b, {required bool keepRemovals}) {
  final out = <String, String>{
    for (final entry in (b ?? const {}).entries)
      if (entry.value.isNotEmpty || keepRemovals) entry.key: entry.value,
  };
  for (final entry in (a ?? const <String, String>{}).entries) {
    if (!(b?.containsKey(entry.key) ?? false)) out[entry.key] = entry.value;
  }
  return out.isEmpty ? null : out;
}

Attributes? _transformAttributes(Attributes? a, Attributes? b, {required bool thisFirst}) {
  if (!thisFirst || a == null || a.isEmpty) return b;
  final out = <String, String>{
    for (final entry in (b ?? const {}).entries)
      if (!a.containsKey(entry.key)) entry.key: entry.value,
  };
  return out.isEmpty ? null : out;
}

Attributes? _invertAttributes(Attributes changed, Attributes? base) {
  final out = <String, String>{
    for (final key in changed.keys)
      if (base?[key] != changed[key]) key: base?[key] ?? '',
  };
  return out.isEmpty ? null : out;
}

enum _Kind { insert, delete, retain }

/// Walks a delta op by op, handing out pieces of the length asked; past the
/// end it hands out an endless retain.
class _Iterator {
  _Iterator(this._ops);

  // 2^53 - 1, not 1 << 53: shifts are 32-bit on the web
  static const _infinite = 0x1FFFFFFFFFFFFF;

  final List<Op> _ops;
  var _index = 0;
  var _offset = 0;

  bool get hasNext => _index < _ops.length;

  _Kind get peekType {
    if (_index == _ops.length) return _Kind.retain;
    final op = _ops[_index];
    return op.isInsert ? _Kind.insert : (op.isDelete ? _Kind.delete : _Kind.retain);
  }

  int get peekLength => _index == _ops.length ? _infinite : _ops[_index].length - _offset;

  Op next([int length = _infinite]) {
    if (_index == _ops.length) return Op.retain(length);
    final op = _ops[_index];
    final offset = _offset;
    final n = math.min(length, op.length - offset);
    if (n == op.length - offset) {
      _index++;
      _offset = 0;
    } else {
      _offset += n;
    }
    if (op.isDelete) return Op.delete(n);
    if (op.isRetain) return Op.retain(n, op.attributes);
    return Op.insert(op.insert!.substring(offset, offset + n), op.attributes);
  }

  void skip(int length) {
    while (length > 0 && hasNext) {
      length -= next(length).length;
    }
  }
}
