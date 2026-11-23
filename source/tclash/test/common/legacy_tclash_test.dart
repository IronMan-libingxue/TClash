import 'dart:io';

import 'package:fl_clash/common/legacy_tclash.dart';
import 'package:fl_clash/enum/enum.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:path/path.dart' as p;

void main() {
  test(
    'keeps the current effective rules, subscription and selection',
    () async {
      final root = await Directory.systemTemp.createTemp('tclash-legacy');
      addTearDown(() => root.delete(recursive: true));
      final directory = await Directory(p.join(root.path, 'profiles')).create();
      final source = File(p.join(directory.path, 'fixture.yaml'));
      const original = 'proxy-groups: []\nrules: [MATCH,DIRECT]\n';
      await source.writeAsString(original);
      final effective = File(p.join(root.path, 'clash-verge.yaml'));
      await effective.writeAsString(
        'proxy-groups: []\nrules: [MATCH,REJECT]\n',
      );

      final manifest = {
        'current': 'test-device-profile',
        'items': [
          {
            'uid': 'test-device-profile',
            'type': 'remote',
            'file': 'fixture.yaml',
            'url': 'https://example.invalid/profile',
            'selected': [
              {'name': 'Service', 'now': 'Existing node'},
            ],
          },
          {'uid': 'bad', 'type': 'local', 'file': '../clash-verge.yaml'},
        ],
      };
      final upgrade = LegacyTClash(root);
      final result = await upgrade.readProfiles(manifest);
      expect(result, hasLength(2));
      expect(result.first.source.path, effective.path);
      expect(result.first.profile.selectedMap, {'Service': 'Existing node'});
      expect(result.last.profile.type, ProfileType.url);
      expect(result.last.profile.autoUpdate, isFalse);
      expect(result.last.source.path, source.path);
      expect(await source.readAsString(), original);
      final retry = await upgrade.readProfiles(manifest);
      expect(
        retry.map((row) => row.profile.id),
        result.map((row) => row.profile.id),
      );

      await effective.writeAsString(original);
      final equivalent = await upgrade.readProfiles(manifest);
      expect(equivalent.single.profile.type, ProfileType.url);
      expect(equivalent.single.profile.autoUpdate, isTrue);
    },
  );
}
