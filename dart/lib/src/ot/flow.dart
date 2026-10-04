import 'delta.dart';

/// A flow kept paragraph by paragraph, as the Go package `ot` keeps it, in
/// blocks of paragraphs: an edit rebuilds the paragraphs it touches and the
/// blocks holding them, and shares the others with the flow it was made
/// from. A keystroke in a long note costs a paragraph and a block, not the
/// note.
class Flow {
  Flow._(this._blocks, this.length);

  /// The flow of [delta], null unless it is one: inserts ending with a mark.
  static Flow? of(Delta delta) {
    final paras = _paragraphs(delta.ops);
    if (paras == null || paras.isEmpty) return null;
    final blocks = _blocksOf(paras);
    return Flow._(blocks, blocks.fold(0, (n, b) => n + b.length)).._delta = delta;
  }

  final List<_Block> _blocks;

  /// The length of the flow, marks included.
  final int length;

  Delta? _delta;

  /// The whole flow as one delta, made the first time it is asked for.
  Delta get delta => _delta ??= Delta([
    for (final b in _blocks)
      for (final p in b.paras) ...p.ops,
  ]);

  /// The flow [delta] makes of this one, and the delta that undoes it; null
  /// when it does not apply: longer than the flow, removing its last mark
  /// or leaving text after it.
  (Flow, Delta)? apply(Delta delta) {
    if (delta.baseLength > length || _deletesLast(delta)) return null;
    final ops = [...delta.ops];
    while (ops.isNotEmpty && ops.last.isRetain && ops.last.attributes == null) {
      ops.removeLast();
    }
    if (ops.isEmpty) return (this, Delta());
    var start = 0;
    if (ops.first.isRetain && ops.first.attributes == null) start = ops.removeAt(0).retain;
    final end = start + ops.fold<int>(0, (n, op) => n + op.delete + op.retain);

    // from the paragraph where the edit starts to the one holding the first
    // unit it leaves alone: deleting a mark joins the next paragraph
    final (fb, fi, offset) = _locate(start);
    final (lb, li, _) = _locate(end);
    final touched = [
      for (var b = fb; b <= lb; b++)
        for (var i = b == fb ? fi : 0; i <= (b == lb ? li : _blocks[b].paras.length - 1); i++) _blocks[b].paras[i],
    ];
    final region = touched.length == 1 ? touched.first : Delta([for (final p in touched) ...p.ops]);
    final local = Delta([Op.retain(start - offset), ...ops]);
    final paras = _paragraphs(region.compose(local).ops);
    if (paras == null || paras.isEmpty) return null;
    final undo = Delta([Op.retain(offset), ...local.invert(region).ops]).chop();
    final rebuilt = _blocksOf([..._blocks[fb].paras.sublist(0, fi), ...paras, ..._blocks[lb].paras.sublist(li + 1)]);
    return (Flow._([..._blocks.sublist(0, fb), ...rebuilt, ..._blocks.sublist(lb + 1)], length + local.change), undo);
  }

  /// The block, the paragraph in it and the paragraph's offset holding
  /// [offset]; the last paragraph past the end.
  (int, int, int) _locate(int offset) {
    var at = 0, b = 0;
    while (b < _blocks.length - 1 && at + _blocks[b].length <= offset) {
      at += _blocks[b++].length;
    }
    final sizes = _blocks[b].sizes;
    var i = 0;
    while (i < sizes.length - 1 && at + sizes[i] <= offset) {
      at += sizes[i++];
    }
    return (b, i, at);
  }

  bool _deletesLast(Delta delta) {
    var at = 0;
    for (final op in delta.ops) {
      if (op.isDelete && at + op.delete == length) return true;
      at += op.delete + op.retain;
    }
    return false;
  }
}

/// Paragraphs that stay together in a flow, a few dozen of them.
class _Block {
  _Block(this.paras) : sizes = [for (final p in paras) p.length];

  final List<Delta> paras;
  final List<int> sizes;
  late final int length = sizes.fold(0, (n, s) => n + s);
}

const _blockSize = 64;

List<_Block> _blocksOf(List<Delta> paras) => [
  for (var i = 0; i < paras.length; i += _blockSize)
    _Block(paras.sublist(i, i + _blockSize < paras.length ? i + _blockSize : paras.length)),
];

/// Inserts split after each mark; null when there is anything but inserts,
/// or text after the last mark.
List<Delta>? _paragraphs(List<Op> ops) {
  final paras = <Delta>[];
  var p = Delta();
  for (final op in ops) {
    if (!op.isInsert) return null;
    final text = op.insert!;
    var from = 0;
    while (from < text.length) {
      final i = text.indexOf('\n', from);
      if (i < 0) {
        p.push(Op.insert(from == 0 ? text : text.substring(from), op.attributes));
        break;
      }
      p.push(Op.insert(from == 0 && i == text.length - 1 ? text : text.substring(from, i + 1), op.attributes));
      paras.add(p);
      p = Delta();
      from = i + 1;
    }
  }
  return p.isEmpty ? paras : null;
}
