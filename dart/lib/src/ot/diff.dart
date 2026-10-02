import 'dart:typed_data';

import 'delta.dart';

/// A delta turning the text [from] into [to], paragraph by paragraph and
/// then character by character within the paragraphs that changed.
///
/// It stands for what others did while this client could not follow edit by
/// edit, to rebase its own edits over it: what matters is where text went.
Delta diff(String from, String to) {
  final out = Delta();
  final a = _lines(from), b = _lines(to);
  var i = 0, j = 0;
  for (final (kind, n) in _myers(a, b, (x, y) => a[x] == b[y], _maxLineEdits)) {
    switch (kind) {
      case _Edit.same:
        out.retain(_length(a, i, n));
      case _Edit.delete:
        out.delete(_length(a, i, n));
      case _Edit.insert:
        out.insert(b.sublist(j, j + n).join());
    }
    if (kind != _Edit.insert) i += n;
    if (kind != _Edit.delete) j += n;
  }
  return _refine(out.chop(), from);
}

const _maxLineEdits = 1000;
const _maxCharEdits = 500;

/// Diffs the characters of the paragraphs replaced by others.
Delta _refine(Delta lines, String from) {
  final out = Delta();
  var i = 0;
  final ops = lines.ops;
  for (var k = 0; k < ops.length; k++) {
    final op = ops[k];
    if (op.isInsert && k + 1 < ops.length && ops[k + 1].isDelete) {
      final deleted = ops[k + 1].delete;
      _chars(out, from.substring(i, i + deleted), op.insert!);
      i += deleted;
      k++;
      continue;
    }
    out.push(op);
    if (!op.isInsert) i += op.length;
  }
  return out.chop();
}

void _chars(Delta out, String a, String b) {
  final edits = _myers(a.codeUnits, b.codeUnits, (x, y) => a.codeUnitAt(x) == b.codeUnitAt(y), _maxCharEdits);
  var i = 0, j = 0;
  for (final (kind, n) in edits) {
    switch (kind) {
      case _Edit.same:
        out.retain(n);
      case _Edit.delete:
        out.delete(n);
      case _Edit.insert:
        out.insert(b.substring(j, j + n));
    }
    if (kind != _Edit.insert) i += n;
    if (kind != _Edit.delete) j += n;
  }
  assert(i == a.length && j == b.length);
}

List<String> _lines(String text) {
  final lines = <String>[];
  var start = 0;
  for (var i = text.indexOf('\n'); i >= 0; i = text.indexOf('\n', start)) {
    lines.add(text.substring(start, i + 1));
    start = i + 1;
  }
  if (start < text.length) lines.add(text.substring(start));
  return lines;
}

int _length(List<String> lines, int from, int n) =>
    lines.sublist(from, from + n).fold(0, (sum, line) => sum + line.length);

enum _Edit { same, delete, insert }

/// Myers' shortest edit script between two sequences, as runs of one kind.
/// Beyond [maxEdits] the middle is replaced whole.
List<(_Edit, int)> _myers(List<Object?> a, List<Object?> b, bool Function(int, int) equal, int maxEdits) {
  var head = 0;
  while (head < a.length && head < b.length && equal(head, head)) {
    head++;
  }
  var tail = 0;
  while (tail < a.length - head && tail < b.length - head && equal(a.length - 1 - tail, b.length - 1 - tail)) {
    tail++;
  }
  final n = a.length - head - tail, m = b.length - head - tail;
  final steps = <_Edit>[];

  final trace = <Int32List>[];
  final offset = maxEdits + 1;
  final v = Int32List(2 * offset + 1);
  var found = n == 0 && m == 0;
  for (var d = 0; d <= maxEdits && !found; d++) {
    trace.add(Int32List.fromList(v.sublist(offset - d, offset + d + 1)));
    for (var k = -d; k <= d; k += 2) {
      var x = k == -d || (k != d && v[offset + k - 1] < v[offset + k + 1]) ? v[offset + k + 1] : v[offset + k - 1] + 1;
      var y = x - k;
      while (x < n && y < m && equal(head + x, head + y)) {
        x++;
        y++;
      }
      v[offset + k] = x;
      if (x >= n && y >= m) {
        found = true;
        break;
      }
    }
  }

  if (!found) {
    steps
      ..addAll(List.filled(n, _Edit.delete))
      ..addAll(List.filled(m, _Edit.insert));
  } else if (n > 0 || m > 0) {
    // trace[d] is the furthest x on each diagonal -d..d before step d
    var x = n, y = m;
    for (var d = trace.length - 1; d > 0; d--) {
      final before = trace[d];
      final k = x - y;
      final down = k == -d || (k != d && before[k - 1 + d] < before[k + 1 + d]);
      final prevK = down ? k + 1 : k - 1;
      final prevX = before[prevK + d];
      final prevY = prevX - prevK;
      while (x > prevX && y > prevY) {
        steps.add(_Edit.same);
        x--;
        y--;
      }
      steps.add(down ? _Edit.insert : _Edit.delete);
      x = prevX;
      y = prevY;
    }
    steps.addAll(List.filled(x, _Edit.same));
    final reversed = steps.reversed.toList();
    steps
      ..clear()
      ..addAll(reversed);
  }

  final runs = <(_Edit, int)>[];
  void add(_Edit kind, int count) {
    if (count == 0) return;
    if (runs.isNotEmpty && runs.last.$1 == kind) {
      runs.last = (kind, runs.last.$2 + count);
    } else {
      runs.add((kind, count));
    }
  }

  add(_Edit.same, head);
  for (final step in steps) {
    add(step, 1);
  }
  add(_Edit.same, tail);
  return runs;
}
