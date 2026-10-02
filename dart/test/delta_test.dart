import 'dart:convert';
import 'dart:io';
import 'dart:math';

import 'package:flutter_test/flutter_test.dart';
import 'package:trame/src/ot/delta.dart';
import 'package:trame/src/ot/diff.dart';

Delta _delta(Object? json) => Delta.fromJson(json ?? const [])!;

void main() {
  test('runs the vectors of the Go package', () {
    final vectors = jsonDecode(File('../testdata/ot/vectors.json').readAsStringSync()) as List<Object?>;
    expect(vectors, hasLength(1200));
    for (final raw in vectors) {
      final v = raw! as Map<String, Object?>;
      final a = _delta(v['a']);
      final first = v['first'] == true;
      final got = switch (v['kind']) {
        'compose' => a.compose(_delta(v['b'])).toJson(),
        'transform' => a.transform(_delta(v['b']), thisFirst: first).toJson(),
        'position' => a.transformPosition(v['pos'] as int? ?? 0, thisFirst: first),
        _ => fail('unknown vector ${v['kind']}'),
      };
      expect(got, v['out'] ?? const <Object>[], reason: jsonEncode(v));
    }
  });

  test('reads only well-formed ops', () {
    expect(Delta.fromJson([
      {'i': 'a', 'a': {'b': '1'}},
      {'r': 2},
      {'d': 1},
    ]), Delta([const Op.insert('a', {'b': '1'}), const Op.retain(2), const Op.delete(1)]));
    for (final bad in [
      [{'i': ''}],
      [{'d': 0}],
      [{'r': -1}],
      [{'i': 'a', 'a': {'b': 1}}],
      [{'x': 1}],
      {'i': 'a'},
    ]) {
      expect(Delta.fromJson(bad), isNull, reason: jsonEncode(bad));
    }
  });

  test('inverts edits', () {
    final random = Random(1);
    for (var i = 0; i < 2000; i++) {
      final base = _flow(random);
      final edit = _edit(random, base);
      final undo = edit.invert(base);
      expect(base.compose(edit).compose(undo), base, reason: '$base $edit $undo');
    }
  });

  test('diffs by paragraph, then by character', () {
    expect(diff('one\ntwo\nthree\n', 'one\ntwo\nthree\n'), Delta());
    expect(diff('one\ntwo\n', 'one\nTwo!\n'), Delta([
      const Op.retain(4),
      const Op.insert('T'),
      const Op.delete(1),
      const Op.retain(2),
      const Op.insert('!'),
    ]));
    expect(diff('a\nb\nc\n', 'a\nc\nd\n'), Delta([
      const Op.retain(2),
      const Op.delete(2),
      const Op.retain(2),
      const Op.insert('d\n'),
    ]));

    final random = Random(2);
    for (var i = 0; i < 2000; i++) {
      final from = _flow(random).text;
      final to = from.length > 1 ? Delta([Op.insert(from)]).compose(_edit(random, Delta([Op.insert(from)]))).text : from;
      expect(Delta([Op.insert(from)]).compose(diff(from, to)).text, to, reason: '$from → $to');
    }
  });

  test('keeps text typed offline where it was, after others edited elsewhere', () {
    const old = 'title\nfirst paragraph\nsecond paragraph\n';
    const now = 'Title\nfirst paragraph\nnew one\nsecond paragraph, edited\n';
    final typed = Delta([const Op.retain(12), const Op.insert('the ')]);
    final rebased = diff(old, now).transform(typed, thisFirst: true);
    expect(Delta([const Op.insert(now)]).compose(rebased).text, 'Title\nfirst the paragraph\nnew one\nsecond paragraph, edited\n');
  });
}

const _alphabet = ['a', 'b', ' ', 'é', '中', '😀', '\n'];

Delta _flow(Random random) {
  final d = Delta();
  for (var i = random.nextInt(6); i > 0; i--) {
    d.insert(_text(random), random.nextBool() ? {'b': '1'} : null);
  }
  d.insert('\n');
  return d;
}

String _text(Random random) => [
  for (var i = 1 + random.nextInt(4); i > 0; i--) _alphabet[random.nextInt(_alphabet.length)],
].join();

/// An edit over whole characters of a flow.
Delta _edit(Random random, Delta flow) {
  final chars = flow.text.runes.map((r) => r > 0xFFFF ? 2 : 1).toList();
  final d = Delta();
  for (var i = 0; i < chars.length && random.nextInt(8) > 0;) {
    var n = 0;
    for (var k = 1 + random.nextInt(3); k > 0 && i < chars.length; k--) {
      n += chars[i++];
    }
    switch (random.nextInt(4)) {
      case 0:
        d.retain(n, {'b': ['', '1', '2'][random.nextInt(3)]});
      case 1:
        d.delete(n);
      case 2:
        d.retain(n);
      default:
        d
          ..insert(_text(random))
          ..retain(n);
    }
  }
  return d;
}
