import 'package:flutter/foundation.dart';

/// The size of a sheet in Excel.
const maxRows = 1 << 20;
const maxCols = 1 << 14;

/// The dimensions rows and columns are inserted and removed along.
const dimRows = 'r';
const dimCols = 'c';

/// A cell of a [Grid], or the fields a change sets in it, where a null
/// value removes the field.
@immutable
class Cell {
  const Cell(this.row, this.col, this.fields);

  final int row;
  final int col;
  final Map<String, Object?> fields;

  static Cell? fromJson(Object? json, {required bool set}) {
    if (json is! List<Object?> || json.length != 3) return null;
    final [row, col, fields] = json;
    if (row is! int || col is! int || fields is! Map<String, Object?>) return null;
    if (row < 0 || row > maxRows || col < 0 || col > maxCols) return null;
    if (fields.keys.any((k) => k.isEmpty) || !set && fields.values.contains(null)) return null;
    return Cell(row, col, fields);
  }

  List<Object> toJson() => [row, col, fields];

  Cell _at(int row, int col) => Cell(row, col, fields);

  @override
  bool operator ==(Object other) =>
      other is Cell && other.row == row && other.col == col && jsonEquals(other.fields, fields);

  @override
  int get hashCode => Object.hash(row, col);

  @override
  String toString() => '${toJson()}';
}

/// The cells of a sheet, as the Go package `ot` keeps them: sparse,
/// addressed by row and column counted from 1, each cell a map of JSON
/// fields. Row 0 holds the fields of whole columns, column 0 those of whole
/// rows. A grid never changes: changing one makes another, sharing the rows
/// left alone.
@immutable
class Grid {
  const Grid._(this._rows, this._cells, this.length);

  const Grid.empty() : this._(const [], const [], 0);

  /// A grid of cells that only set fields.
  factory Grid(Iterable<Cell> cells) => const Grid.empty().set(cells);

  final List<int> _rows;
  final List<List<Cell>> _cells;

  /// The number of cells.
  final int length;

  /// The fields of a cell, null when it has none.
  Map<String, Object?>? cell(int row, int col) {
    final i = _search(_rows, row);
    if (i < 0) return null;
    final cells = _cells[i];
    final j = _searchCol(cells, col);
    return j < 0 ? null : cells[j].fields;
  }

  /// All the cells, row by row.
  Iterable<Cell> get cells => _cells.expand((row) => row);

  /// The cells of rows [first] to [last], row by row.
  Iterable<Cell> rows(int first, int last) sync* {
    for (var i = _lowerBound(_rows, first); i < _rows.length && _rows[i] <= last; i++) {
      yield* _cells[i];
    }
  }

  /// The numbers of the rows holding cells, in order.
  List<int> get rowNumbers => _rows;

  /// This grid with [cells] set, field by field.
  Grid set(Iterable<Cell> cells) {
    final rows = [..._rows];
    final byRow = [..._cells];
    var size = length;
    for (final c in cells) {
      final i = _lowerBound(rows, c.row);
      if (i == rows.length || rows[i] != c.row) {
        rows.insert(i, c.row);
        byRow.insert(i, const []);
      }
      final row = [...byRow[i]];
      final j = _lowerBoundCol(row, c.col);
      final found = j < row.length && row[j].col == c.col;
      final fields = {...found ? row[j].fields : const <String, Object?>{}};
      for (final e in c.fields.entries) {
        if (e.value == null) {
          fields.remove(e.key);
        } else {
          fields[e.key] = e.value;
        }
      }
      if (fields.isEmpty) {
        if (found) {
          row.removeAt(j);
          size--;
        }
      } else if (found) {
        row[j] = Cell(c.row, c.col, fields);
      } else {
        row.insert(j, Cell(c.row, c.col, fields));
        size++;
      }
      if (row.isEmpty) {
        rows.removeAt(i);
        byRow.removeAt(i);
      } else {
        byRow[i] = List.unmodifiable(row);
      }
    }
    return Grid._(List.unmodifiable(rows), List.unmodifiable(byRow), size);
  }

  /// This grid with [n] rows or columns inserted at [at], or removed when
  /// [n] is negative; what is pushed past the end of the sheet is dropped.
  Grid shift(String dim, int at, int n) {
    final rows = <int>[];
    final byRow = <List<Cell>>[];
    var size = 0;
    for (var i = 0; i < _rows.length; i++) {
      var cells = _cells[i];
      final int row;
      if (dim == dimRows) {
        final m = movedIndex(_rows[i], at, n, maxRows);
        if (m == null) continue;
        row = m;
        if (row != _rows[i]) cells = List.unmodifiable([for (final c in cells) c._at(row, c.col)]);
      } else {
        row = _rows[i];
        cells = List.unmodifiable([
          for (final c in cells)
            if (movedIndex(c.col, at, n, maxCols) case final col?) c._at(row, col),
        ]);
        if (cells.isEmpty) continue;
      }
      rows.add(row);
      byRow.add(cells);
      size += cells.length;
    }
    return Grid._(List.unmodifiable(rows), List.unmodifiable(byRow), size);
  }

  static int _search(List<int> rows, int row) {
    final i = _lowerBound(rows, row);
    return i < rows.length && rows[i] == row ? i : -1;
  }

  static int _searchCol(List<Cell> cells, int col) {
    final j = _lowerBoundCol(cells, col);
    return j < cells.length && cells[j].col == col ? j : -1;
  }

  static int _lowerBound(List<int> list, int value) {
    var lo = 0, hi = list.length;
    while (lo < hi) {
      final mid = (lo + hi) >> 1;
      if (list[mid] < value) {
        lo = mid + 1;
      } else {
        hi = mid;
      }
    }
    return lo;
  }

  static int _lowerBoundCol(List<Cell> cells, int col) {
    var lo = 0, hi = cells.length;
    while (lo < hi) {
      final mid = (lo + hi) >> 1;
      if (cells[mid].col < col) {
        lo = mid + 1;
      } else {
        hi = mid;
      }
    }
    return lo;
  }
}

/// Where index [i] goes when [n] rows or columns are inserted at [at], or
/// removed when [n] is negative; null once removed or pushed past [limit].
int? movedIndex(int i, int at, int n, int limit) {
  if (i < at) return i;
  if (n < 0 && i < at - n) return null;
  if (i + n > limit) return null;
  return i + n;
}

bool jsonEquals(Object? a, Object? b) {
  if (a is Map && b is Map) {
    return a.length == b.length && a.keys.every((k) => b.containsKey(k) && jsonEquals(a[k], b[k]));
  }
  if (a is List && b is List) {
    return a.length == b.length && Iterable<int>.generate(a.length).every((i) => jsonEquals(a[i], b[i]));
  }
  return a == b;
}
