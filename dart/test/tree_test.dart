import 'dart:convert';
import 'dart:io';
import 'dart:math';

import 'package:flutter_test/flutter_test.dart';
import 'package:trame/src/ot/delta.dart';
import 'package:trame/src/ot/grid.dart';
import 'package:trame/src/ot/tree.dart';

Edit _edit(Object? json) => Edit.fromJson(json ?? const [])!;

Tree _tree(Object? json) => Tree.fromEdit(_edit(json))!;

void main() {
  test('runs the tree vectors of the Go package', () {
    final vectors = jsonDecode(File('../testdata/ot/tree.json').readAsStringSync()) as List<Object?>;
    expect(vectors, hasLength(800));
    for (final raw in vectors) {
      final v = raw! as Map<String, Object?>;
      final Object got;
      switch (v['kind']) {
        case 'transform':
          got = _edit(v['a']).transform(_edit(v['b']), thisFirst: v['first'] == true).toJson();
        case 'apply':
          final tree = _tree(v['nodes']);
          expect(tree.apply(_edit(v['a'])), isNotNull, reason: jsonEncode(v));
          expect(tree.apply(_edit(v['b'])), isNotNull, reason: jsonEncode(v));
          got = tree.toEdit().toJson();
        case 'key':
          final keys = v['keys']! as List<Object?>;
          got = keyBetween(keys[0]! as String, keys[1]! as String);
        default:
          fail('unknown vector ${v['kind']}');
      }
      expect(jsonDecode(jsonEncode(got)), v['out'] ?? const <Object>[], reason: jsonEncode(v));
    }
  });

  test('reads only well-formed changes', () {
    for (final bad in [
      {'o': 'mov', 'id': 'a'},
      {'o': 'del', 'id': ''},
      {'o': 'del', 'id': 'a b'},
      {'o': 'del', 'id': 'a', 'k': 'V'},
      {'o': 'new', 'id': 'a', 'k': 'V'},
      {'o': 'new', 'id': 'a', 't': 't'},
      {'o': 'new', 'id': 'a', 't': 't', 'k': 'V', 'x': [{'i': 'no mark'}]},
      {'o': 'new', 'id': 'a', 't': 't', 'k': 'V', 'a': {'x': null}},
      {'o': 'set', 'id': 'a'},
      {'o': 'set', 'id': 'a', 'k': 'é'},
      {'o': 'txt', 'id': 'a', 'x': [{'r': -1}]},
    ]) {
      expect(Change.fromJson(bad), isNull, reason: jsonEncode(bad));
    }
  });

  test('applies edits whole or not at all, and inverts them', () {
    final tree = _tree([
      {'o': 'new', 'id': 's1', 't': 'slide', 'k': 'V'},
      {'o': 'new', 'id': 'a', 't': 'shape', 'p': 's1', 'k': 'V', 'a': {'x': 1}, 'x': [{'i': 'ab\n'}]},
      {'o': 'new', 'id': 'b', 't': 'shape', 'p': 's1', 'k': 'F'},
    ]);
    expect(tree.children('s1').map((n) => n.id), ['b', 'a']);
    expect(tree.length, 6);
    final before = tree.toEdit();
    for (final bad in [
      [
        {'o': 'txt', 'id': 'a', 'x': [{'r': 1}, {'i': 'x'}]},
        {'o': 'txt', 'id': 'a', 'x': [{'r': 5}]},
      ],
      [
        {'o': 'del', 'id': 's1'},
        {'o': 'new', 'id': 'c', 't': 'shape', 'k': 'V'},
        {'o': 'new', 'id': 'c', 't': 'shape', 'k': 'V'},
      ],
      [
        {'o': 'txt', 'id': 'a', 'x': [{'r': 2}, {'d': 1}]},
      ],
    ]) {
      expect(tree.apply(_edit(bad)), isNull, reason: jsonEncode(bad));
      expect(tree.toEdit(), before);
      expect(tree.length, 6);
    }

    final undo = tree.apply(_edit([
      {'o': 'set', 'id': 'a', 'k': '0V', 'a': {'x': null, 'y': 'z'}},
      {'o': 'txt', 'id': 'a', 'x': [{'r': 2}, {'i': '\nc'}]},
      {'o': 'del', 'id': 'b'},
    ]))!;
    expect(tree.children('s1').single.attributes, {'y': 'z'});
    expect(tree.apply(undo), isNotNull);
    expect(tree.toEdit(), before);
    expect(tree.apply(_edit([{'o': 'del', 'id': 's1'}])), isNotNull);
    expect(tree.length, 0);
  });

  test('inverts and diffs random edits', () {
    final random = Random(3);
    for (var i = 0; i < 2000; i++) {
      final base = _randomTree(random);
      final edit = _randomEdit(random, base.copy());
      final tree = base.copy();
      final undo = tree.apply(edit);
      expect(undo, isNotNull, reason: '$edit');
      final changed = tree.copy();
      expect(tree.apply(undo!), isNotNull);
      expect(tree.toEdit(), base.toEdit(), reason: 'undo of $edit');
      expect(changed.apply(diffTrees(changed, base)), isNotNull);
      expect(changed.toEdit(), base.toEdit(), reason: 'diff back from $edit');
    }
  });

  test('keys sort between their neighbours', () {
    final random = Random(4);
    final keys = [keyBetween('', '')];
    for (var i = 0; i < 2000; i++) {
      final at = random.nextInt(keys.length + 1);
      final a = at > 0 ? keys[at - 1] : '', b = at < keys.length ? keys[at] : '';
      final k = keyBetween(a, b);
      expect(k.compareTo(a) > 0 && (b.isEmpty || k.compareTo(b) < 0) && !k.endsWith('0'), isTrue, reason: '$a $b $k');
      keys.insert(at, k);
    }
  });
}

var _next = 0;

Tree _randomTree(Random random) {
  final changes = <Change>[];
  for (var i = 0; i < 1 + random.nextInt(3); i++) {
    final top = _randomNode(random, '');
    changes.add(Change.create(top));
    for (var j = 0; j < random.nextInt(3); j++) {
      changes.add(Change.create(_randomNode(random, top.id)));
    }
  }
  return Tree.fromEdit(Edit(changes))!;
}

Node _randomNode(Random random, String parent) {
  final kind = random.nextInt(3);
  return Node(
    id: 'n${_next++}',
    type: 't',
    parent: parent,
    key: ['F', 'V', 'k'][random.nextInt(3)],
    attributes: random.nextBool() ? {'x': random.nextInt(3)} : const {},
    text: kind == 0 ? Delta([Op.insert('${'ab\n' * random.nextInt(3)}c\n')]) : null,
    grid: kind == 1 ? Grid(_randomCells(random, set: false)) : null,
  );
}

List<Cell> _randomCells(Random random, {required bool set}) {
  final cells = <(int, int), Cell>{};
  for (var i = 0; i < 1 + random.nextInt(3); i++) {
    final row = random.nextInt(5), col = random.nextInt(4);
    cells[(row, col)] = Cell(row, col, {
      'v': random.nextInt(3),
      if (random.nextBool()) 's': set && random.nextBool() ? null : 'x${random.nextInt(2)}',
    });
  }
  return cells.values.toList();
}

/// A random edit, which it applies to [tree].
Edit _randomEdit(Random random, Tree tree) {
  var edit = Edit();
  for (var i = 0; i < 1 + random.nextInt(3); i++) {
    final nodes = tree.nodes.toList();
    final Change c;
    if (nodes.isEmpty || random.nextInt(5) == 0) {
      c = Change.create(_randomNode(random, nodes.isEmpty || random.nextBool() ? '' : nodes[random.nextInt(nodes.length)].id));
    } else {
      final node = nodes[random.nextInt(nodes.length)];
      c = switch (random.nextInt(4)) {
        0 => Change.delete(node.id),
        1 => Change.set(node.id, key: 'a', attributes: {'x': null, 'y': 1}),
        _ when node.text != null => Change.text(node.id, Delta([Op.retain(random.nextInt(node.text!.length)), const Op.insert('z\n')])),
        _ when node.grid != null => switch (random.nextInt(3)) {
          0 => Change.insert(node.id, random.nextBool() ? dimRows : dimCols, 1 + random.nextInt(4), 1 + random.nextInt(2)),
          1 => Change.remove(node.id, random.nextBool() ? dimRows : dimCols, 1 + random.nextInt(4), 1 + random.nextInt(2)),
          _ => Change.cells(node.id, _randomCells(random, set: true)),
        },
        _ => Change.set(node.id, attributes: {'z': [1]}),
      };
    }
    if (tree.apply(Edit([c])) != null) edit = edit.compose(Edit([c]));
  }
  return edit;
}
