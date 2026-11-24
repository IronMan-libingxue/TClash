import 'dart:convert';
import 'dart:io';
import 'dart:isolate';
import 'dart:typed_data';

import 'package:crypto/crypto.dart';
import 'package:path/path.dart' as p;
import 'package:yaml/yaml.dart';

const _magic = [84, 67, 76, 65, 83, 72, 0, 1, 80];
const _digestLength = 32;
const _maxPackageBytes = 32 * 1024 * 1024;
const _attachments = ['rules', 'proxies', 'groups', 'merge', 'script'];
const _legacyScriptHeader = '// TClash Preview .tclash profile\n';

class TClashProfilePackage {
  final String name;
  final String url;
  final bool autoUpdate;
  final Duration? updateInterval;
  final Uint8List config;
  final String? script;

  TClashProfilePackage._({
    required this.name,
    required this.url,
    required this.autoUpdate,
    required this.updateInterval,
    required this.config,
    required this.script,
  });

  static bool isExtension(String path) =>
      p.extension(path).toLowerCase() == '.tclash';

  static bool isLegacyScript(String? script) =>
      script?.startsWith(_legacyScriptHeader) ?? false;

  static Future<TClashProfilePackage> readPath(String path) async {
    if (!isExtension(path) || !await FileSystemEntity.isFile(path)) {
      throw const FormatException('Invalid TClash profile file');
    }
    return read(p.basename(path), File(path).openRead);
  }

  static Future<TClashProfilePackage> read(
    String name,
    Stream<List<int>> Function() stream,
  ) async {
    if (!isExtension(name)) {
      throw const FormatException('Invalid TClash profile extension');
    }
    final bytes = BytesBuilder(copy: false);
    await for (final chunk in stream()) {
      if (bytes.length + chunk.length > _maxPackageBytes) {
        throw const FormatException('TClash profile is too large');
      }
      bytes.add(chunk);
    }
    final payload = bytes.takeBytes();
    return Isolate.run(() => decode(name, payload));
  }

  static TClashProfilePackage decode(String fileName, List<int> bytes) {
    final offset = _magic.length + _digestLength;
    if (!isExtension(fileName) ||
        bytes.length <= offset ||
        bytes.length > _maxPackageBytes) {
      throw const FormatException('Invalid TClash profile file');
    }
    for (var i = 0; i < _magic.length; i++) {
      if (bytes[i] != _magic[i]) {
        throw const FormatException('Invalid TClash profile header');
      }
    }
    final data = Uint8List(bytes.length - offset);
    for (var i = 0; i < data.length; i++) {
      data[i] = bytes[offset + i] ^ 0xa5;
    }
    final digest = sha256.convert(data).bytes;
    for (var i = 0; i < digest.length; i++) {
      if (digest[i] != bytes[_magic.length + i]) {
        throw const FormatException('Invalid TClash profile checksum');
      }
    }

    final package = jsonDecode(utf8.decode(data));
    if (package is! Map<String, dynamic> ||
        package['version'] is! int ||
        package['version'] != 1 ||
        package['root'] is! String ||
        package['records'] is! List) {
      throw const FormatException('Unsupported TClash profile version');
    }
    final records = package['records'] as List;
    if (records.isEmpty || records.length > _attachments.length + 1) {
      throw const FormatException('Invalid TClash profile records');
    }
    final byId = <String, ({Map<String, dynamic> item, String content})>{};
    for (final record in records) {
      if (record is! Map<String, dynamic> ||
          record['item'] is! Map<String, dynamic> ||
          record['content'] is! String) {
        throw const FormatException('Invalid TClash profile record');
      }
      final item = record['item'] as Map<String, dynamic>;
      final id = item['uid'];
      if (id is! String || id.isEmpty || byId.containsKey(id)) {
        throw const FormatException('Invalid TClash profile ID');
      }
      byId[id] = (item: item, content: record['content'] as String);
    }
    final root = byId.remove(package['root']);
    final type = root?.item['type'];
    if (root == null || (type != 'local' && type != 'remote')) {
      throw const FormatException('Missing TClash profile root');
    }
    configMap(root.content);
    final option = root.item['option'];
    if (option != null && option is! Map<String, dynamic>) {
      throw const FormatException('Invalid TClash profile options');
    }
    final options = option as Map<String, dynamic>? ?? const {};
    final linked = <String, String>{};
    for (final kind in _attachments) {
      final id = options[kind];
      if (id == null) continue;
      final record = byId.remove(id);
      if (record == null || record.item['type'] != kind) {
        throw const FormatException('Invalid TClash profile attachment');
      }
      linked[kind] = record.content;
    }
    if (byId.isNotEmpty) {
      throw const FormatException('Unlinked TClash profile record');
    }

    var url = '';
    if (type == 'remote') {
      final source = root.item['url'];
      final uri = source is String ? Uri.tryParse(source) : null;
      if (uri == null ||
          !const {'http', 'https'}.contains(uri.scheme) ||
          uri.host.isEmpty) {
        throw const FormatException('Invalid TClash subscription URL');
      }
      url = source as String;
    }
    final interval = options['update_interval'];
    final name = root.item['name'];
    return TClashProfilePackage._(
      name: name is String && name.trim().isNotEmpty
          ? name
          : p.basenameWithoutExtension(fileName),
      url: url,
      autoUpdate: options['allow_auto_update'] != false,
      updateInterval: interval is int && interval > 0 && interval <= 525600
          ? Duration(minutes: interval)
          : null,
      config: Uint8List.fromList(utf8.encode(root.content)),
      script: _buildScript(linked, name is String ? name : ''),
    );
  }

  static Map<String, dynamic> configMap(String content) {
    final mapping = loadYaml(content);
    if (mapping is! Map || mapping.keys.any((key) => key is! String)) {
      throw const FormatException('Invalid TClash configuration');
    }
    return Map<String, dynamic>.from(mapping);
  }

  static Map<String, dynamic> configBytesMap(List<int> bytes) =>
      configMap(utf8.decode(bytes));

  static String? _buildScript(Map<String, String> linked, String profileName) {
    if (linked.isEmpty) return null;
    final sequences = <String, Map<String, dynamic>>{};
    for (final (kind, field) in [
      ('rules', 'rules'),
      ('proxies', 'proxies'),
      ('groups', 'proxy-groups'),
    ]) {
      final yaml = linked[kind];
      if (yaml == null) continue;
      final sequence = configMap(yaml);
      if (sequence['prepend'] is! List ||
          sequence['append'] is! List ||
          sequence['delete'] is! List ||
          (sequence['delete'] as List).any((item) => item is! String)) {
        throw const FormatException('Invalid TClash profile sequence');
      }
      sequences[field] = sequence;
    }
    final mergeText = linked['merge'];
    final merge = mergeText == null || loadYaml(mergeText) == null
        ? <String, dynamic>{}
        : configMap(mergeText);
    final script = linked['script']?.trim();
    if (linked.containsKey('script') && (script == null || script.isEmpty)) {
      throw const FormatException('Empty TClash profile script');
    }
    final overlays = jsonEncode(
      jsonEncode({'sequences': sequences, 'merge': merge}),
    );
    final start =
        '$_legacyScriptHeader'
        'function main(config) {\n'
        '  const overlays = JSON.parse($overlays);\n'
        '$_legacyEnhancement\n';
    if (script == null) return '$start  return config;\n}';
    return '$start'
        '  const apply = (function() {\n$script\n    return main;\n  })();\n'
        '  const normalized = Object.fromEntries(Object.entries(config).map('
        '([key, value]) => [key.toLowerCase(), value]));\n'
        '  return apply(normalized, ${jsonEncode(profileName)});\n}';
  }
}

// The order, DNS/hosts merge and selector behavior mirror TClash Preview.
const _legacyEnhancement = r'''
  const named = item => typeof item === 'string' ? item : item?.name;
  const isMap = value => value !== null && typeof value === 'object' && !Array.isArray(value);
  const set = (map, key, value) => Object.defineProperty(map, key, {
    enumerable: true, configurable: true, writable: true, value
  });
  const merge = (base, overlay, shallow = false) => {
    const result = {...base};
    for (const [key, value] of Object.entries(overlay)) {
      const old = Object.hasOwn(result, key) ? result[key] : null;
      set(result, key, !shallow && isMap(old) && isMap(value) ? merge(old, value) : value);
    }
    return result;
  };
  for (const [field, edit] of Object.entries(overlays.sequences)) {
    const existing = Array.isArray(config[field]) ? config[field] : [];
    const removed = new Set(edit.delete);
    config[field] = [...edit.prepend, ...existing.filter(item => !removed.has(named(item))), ...edit.append];
    if (field !== 'proxies' || !Array.isArray(config['proxy-groups'])) continue;
    const added = [...new Set([...edit.prepend, ...edit.append].map(named).filter(name => typeof name === 'string'))];
    let assigned = false;
    for (const group of config['proxy-groups']) {
      if (!isMap(group)) continue;
      const members = Array.isArray(group.proxies) ? group.proxies.filter(item => typeof item !== 'string' || !removed.has(item)) : null;
      if (!assigned && added.length && ['select', 'selector'].includes(String(group.type).toLowerCase())) {
        const seen = new Set(added);
        group.proxies = [...added];
        for (const item of members ?? []) {
          if (typeof item === 'string' && seen.has(item)) continue;
          group.proxies.push(item);
          if (typeof item === 'string') seen.add(item);
        }
        assigned = true;
      } else if (members !== null) {
        group.proxies = members;
      }
    }
  }
  for (const [key, value] of Object.entries(overlays.merge)) {
    const field = key.toLowerCase();
    const old = Object.hasOwn(config, field) ? config[field] : null;
    set(config, field, field !== 'hosts' && isMap(old) && isMap(value)
      ? merge(old, value, field === 'dns')
      : value);
  }
''';
