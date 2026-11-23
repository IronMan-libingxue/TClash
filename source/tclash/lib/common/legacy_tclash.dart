import 'dart:convert';
import 'dart:io';

import 'package:collection/collection.dart';
import 'package:crypto/crypto.dart';
import 'package:fl_clash/common/common.dart';
import 'package:fl_clash/database/database.dart';
import 'package:fl_clash/enum/enum.dart';
import 'package:fl_clash/models/models.dart';
import 'package:path/path.dart' as p;
import 'package:yaml/yaml.dart';

typedef LegacyTClashProfile = ({Profile profile, File source, String legacyId});

class LegacyTClash {
  const LegacyTClash(this.root);

  final Directory root;

  static LegacyTClash? get forCurrentUser {
    if (safeModeBuild) return null;
    final root = Platform.isMacOS
        ? p.join(
            Platform.environment['HOME'] ?? '',
            'Library',
            'Application Support',
          )
        : Platform.isWindows
        ? Platform.environment['APPDATA']
        : null;
    if (root == null || !p.isAbsolute(root)) return null;
    return LegacyTClash(Directory(p.join(root, 'local.tclash.preview')));
  }

  Future<Config> apply(Config config) async {
    if ((await database.profilesDao.ids().get()).isNotEmpty) return config;
    final manifest = await _yaml(_file('profiles.yaml'));
    final current = manifest['current'];
    final profiles = await readProfiles(manifest);
    if (profiles.isEmpty) return config;
    final selected = profiles.firstWhereOrNull(
      (row) => row.legacyId == current,
    );
    final previous = await _yaml(_file('verge.yaml'));
    final network = await _yaml(_file('config.yaml'));

    for (final entry in profiles) {
      final destination = File(
        await appPath.getProfilePath('${entry.profile.id}'),
      );
      await destination.parent.create(recursive: true);
      await destination.writeAsStringAtomically(
        await entry.source.readAsString(),
      );
    }
    final proxy = previous['enable_system_proxy'] == true;
    final tun = previous['enable_tun_mode'] == true;
    final patch = config.patchClashConfig;
    final mode = Mode.values.firstWhereOrNull(
      (mode) => mode.name == network['mode'],
    );
    final upgraded = config.copyWith(
      currentProfileId: selected?.profile.id,
      networkProps: config.networkProps.copyWith(systemProxy: proxy),
      appSettingProps: config.appSettingProps.copyWith(
        autoLaunch: previous['enable_auto_launch'] == true,
        autoRun: selected != null && (proxy || tun),
      ),
      patchClashConfig: patch.copyWith(
        mode: mode ?? patch.mode,
        mixedPort: _port(network['mixed-port'], patch.mixedPort),
        socksPort: _port(network['socks-port'], patch.socksPort),
        port: _port(network['port'], patch.port),
        allowLan: network['allow-lan'] == true,
        ipv6: network['ipv6'] == true,
        tun: patch.tun.copyWith(enable: tun),
      ),
    );
    await database.transaction(() async {
      await database.profilesDao.setAll(profiles.map((row) => row.profile));
      if (!await preferences.saveConfig(upgraded)) {
        throw const FileSystemException();
      }
    });
    return upgraded;
  }

  Future<List<LegacyTClashProfile>> readProfiles(
    Map<Object?, Object?> manifest,
  ) async {
    final result = <LegacyTClashProfile>[];
    final items = manifest['items'];
    if (items is! List) return result;
    for (final item in items.whereType<Map>()) {
      final type = item['type'];
      final uid = item['uid'];
      final file = item['file'];
      if ((type != 'local' && type != 'remote') ||
          uid is! String ||
          uid.isEmpty ||
          file is! String ||
          p.basename(file) != file ||
          !file.toLowerCase().endsWith('.yaml')) {
        continue;
      }
      final original = File(p.join(root.path, 'profiles', file));
      if (!await original.exists()) continue;
      final effective = uid == manifest['current']
          ? await _effective(original)
          : null;
      final uri = Uri.tryParse(item['url']?.toString() ?? '');
      final url =
          uri != null &&
              (uri.scheme == 'https' || uri.scheme == 'http') &&
              uri.host.isNotEmpty
          ? uri.toString()
          : '';
      final interval = _object(item['option'])['update_interval'];
      final updated = item['updated'];
      final extra = _object(item['extra']);
      final label = item['name'] is String ? item['name']! as String : file;
      final selected = item['selected'];
      final profile = Profile(
        id: _id(uid),
        label: label,
        order: result.length,
        type: url.isEmpty || effective != null
            ? ProfileType.file
            : ProfileType.url,
        url: effective == null ? url : '',
        autoUpdate:
            url.isNotEmpty &&
            effective == null &&
            _object(item['option'])['allow_auto_update'] != false,
        autoUpdateDuration: interval is int && interval > 0
            ? Duration(minutes: interval)
            : defaultUpdateDuration,
        lastUpdateDate: updated is int && updated > 0 && updated < 4102444800
            ? DateTime.fromMillisecondsSinceEpoch(updated * 1000)
            : null,
        subscriptionInfo: extra.isEmpty
            ? null
            : SubscriptionInfo(
                upload: (extra['upload'] as int?) ?? 0,
                download: (extra['download'] as int?) ?? 0,
                total: (extra['total'] as int?) ?? 0,
                expire: (extra['expire'] as int?) ?? 0,
              ),
        selectedMap: {
          if (selected is List)
            for (final choice in selected.whereType<Map>())
              if (choice['name'] is String && choice['now'] is String)
                choice['name']! as String: choice['now']! as String,
        },
      );
      result.add((
        profile: profile,
        source: effective ?? original,
        legacyId: uid,
      ));
      if (effective != null && url.isNotEmpty) {
        result.add((
          profile: profile.copyWith(
            id: _id('$uid:subscription'),
            type: ProfileType.url,
            url: url,
            autoUpdate: false,
            order: result.length,
          ),
          source: original,
          legacyId: '$uid:subscription',
        ));
      }
    }
    return result;
  }

  Future<File?> _effective(File original) async {
    final runtime = _file('clash-verge.yaml');
    final data = await _yaml(runtime);
    if (data['rules'] is! List || data['proxy-groups'] is! List) return null;
    final source = await _yaml(original);
    const equality = DeepCollectionEquality();
    for (final field in [
      'proxy-groups',
      'proxies',
      'rules',
      'rule-providers',
      'proxy-providers',
    ]) {
      if (!equality.equals(data[field], source[field])) return runtime;
    }
    return null;
  }

  File _file(String name) => File(p.join(root.path, name));

  static int _id(String legacy) => int.parse(
    sha256.convert(utf8.encode(legacy)).toString().substring(0, 12),
    radix: 16,
  );

  static int _port(Object? value, int fallback) =>
      value is int && value >= 0 && value <= 65535 ? value : fallback;

  static Map<Object?, Object?> _object(Object? value) =>
      value is Map<Object?, Object?> ? value : const {};

  static Future<Map<Object?, Object?>> _yaml(File file) async {
    if (!await file.exists()) return const {};
    if (await file.length() > 32 << 20) throw const FormatException();
    return _object(loadYaml(await file.readAsString()));
  }
}
