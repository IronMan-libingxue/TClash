import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';

import 'package:crypto/crypto.dart';
import 'package:fl_clash/common/tclash_profile.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:rust_api/rust_api.dart';

import '../plugins/code_forge/support.dart' show initEditorNative;

Map<String, Object?> _record(
  String id,
  String type,
  String content, [
  Map<String, Object?> options = const {},
]) => {
  'item': {'uid': id, 'type': type, ...options},
  'content': content,
};

Uint8List _legacyPackage(
  List<Map<String, Object?>> records, {
  int version = 1,
}) {
  final body = utf8.encode(
    jsonEncode({'version': version, 'root': 'Rroot', 'records': records}),
  );
  return Uint8List.fromList([
    ...'TCLASH\u0000\u0001P'.codeUnits,
    ...sha256.convert(body).bytes,
    for (final byte in body) byte ^ 0xa5,
  ]);
}

void main() {
  const config = '''
proxies: [{name: Old, type: direct}, {name: Keep, type: direct}]
proxy-groups: [{name: Select, type: select, proxies: [Old, Keep]}]
rules: ['MATCH,Select']
dns: {nameserver: ['1.1.1.1'], nameserver-policy: {old.test: '1.0.0.1'}}
''';
  final root = _record('Rroot', 'remote', config, {
    'name': 'Existing subscription',
    'url': 'https://example.invalid/subscription',
    'option': {
      'update_interval': 45,
      'proxies': 'pedit',
      'merge': 'mmerge',
      'script': 'sscript',
    },
  });
  final linked = [
    root,
    _record('pedit', 'proxies', '''
prepend: [{name: New, type: direct}]
append: []
delete: [Old]
'''),
    _record('mmerge', 'merge', '''
dns: {nameserver-policy: {new.test: '8.8.8.8'}}
'''),
    _record('sscript', 'script', '''
function main(config, profileName) {
  if (profileName === 'Existing subscription') {
    config.rules.unshift('DOMAIN,example.test,DIRECT');
  }
  return config;
}
'''),
  ];

  test('picker bytes and desktop file paths decode the old export', () async {
    final path = await Directory.systemTemp.createTemp('tclash-import');
    addTearDown(() => path.delete(recursive: true));
    final bytes = _legacyPackage(linked);
    final file = await File('${path.path}/old.TCLASH').writeAsBytes(bytes);
    final picked = await TClashProfilePackage.read('old.TCLASH', file.openRead);
    final dropped = await TClashProfilePackage.readPath(file.path);
    expect(dropped.config, picked.config);
    expect(dropped.script, picked.script);
    expect(dropped.name, 'Existing subscription');
    expect(dropped.url, 'https://example.invalid/subscription');
    expect(dropped.autoUpdate, isTrue);
    expect(dropped.updateInterval, const Duration(minutes: 45));
  });

  test(
    'rejects renamed YAML, wrong envelopes, corruption and missing links',
    () async {
      final valid = _legacyPackage(linked);
      final broken = Uint8List.fromList(valid)..[80] ^= 1;
      final backup = Uint8List.fromList(valid)..[8] = 'B'.codeUnitAt(0);
      for (final bytes in [
        utf8.encode(config),
        backup,
        broken,
        _legacyPackage([root]),
        _legacyPackage([...linked, linked.last]),
        _legacyPackage(linked, version: 2),
      ]) {
        expect(
          () => TClashProfilePackage.decode('old.tclash', bytes),
          throwsFormatException,
        );
      }
      expect(
        () => TClashProfilePackage.decode('old.yaml', valid),
        throwsFormatException,
      );
      final temp = await Directory.systemTemp.createTemp('renamed-yaml');
      addTearDown(() => temp.delete(recursive: true));
      final file = await File(
        '${temp.path}/plain.tclash',
      ).writeAsString(config);
      await expectLater(
        TClashProfilePackage.readPath(file.path),
        throwsFormatException,
      );
    },
  );

  test(
    'applies legacy proxy, merge and script rules in the existing sandbox',
    () async {
      await initEditorNative();
      final package = TClashProfilePackage.decode(
        'old.tclash',
        _legacyPackage(linked),
      );
      final script = package.script!;
      final result =
          jsonDecode(
                await evaluateScript(
                  script: script,
                  config: jsonEncode(TClashProfilePackage.configMap(config)),
                ),
              )
              as Map<String, dynamic>;
      expect(result['proxies'].map((proxy) => proxy['name']), ['New', 'Keep']);
      expect(result['proxy-groups'].single['proxies'], ['New', 'Keep']);
      expect(result['dns']['nameserver-policy'], {'new.test': '8.8.8.8'});
      expect(result['dns']['nameserver'], ['1.1.1.1']);
      expect(result['rules'], ['DOMAIN,example.test,DIRECT', 'MATCH,Select']);
    },
  );
}
