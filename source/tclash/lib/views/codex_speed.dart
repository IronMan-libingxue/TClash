import 'package:fl_clash/common/codex_usage.dart';
import 'package:fl_clash/common/common.dart';
import 'package:fl_clash/enum/enum.dart';
import 'package:fl_clash/providers/providers.dart';
import 'package:fl_clash/ui/icons/icons.dart';
import 'package:fl_clash/ui/ui.dart';
import 'package:fl_clash/views/dashboard/layout.dart';
import 'package:fl_clash/views/dashboard/widgets/card.dart';
import 'package:fl_clash/widgets/widgets.dart';
import 'package:flutter/widgets.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:intl/intl.dart' show NumberFormat;

class CodexUsageCard extends ConsumerWidget {
  const CodexUsageCard({super.key, this.dashboard = true});

  final bool dashboard;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final usage = ref.watch(codexUsageProvider);
    final labels = context.appLocalizations;
    return ListenableBuilder(
      listenable: usage,
      builder: (context, _) {
        final data = usage.data;
        final speed = usageObject(data['speed']);
        final model = speed['model']?.toString() ?? '';
        final models = usageList(data['displayModels']);
        final displayedModel = models
            .where((row) => row['name'] == model)
            .firstOrNull;
        final activity = currentUsageActivity(data);
        final active = (activity['active'] as int?) ?? 0;
        final idle = speed['state'] == 'idle';
        final rate = speed['rate'] as num?;
        final tier = ((activity['ultrafast'] as int?) ?? 0) > 0
            ? ' · ⚡⚡ Ultrafast'
            : ((activity['fast'] as int?) ?? 0) > 0
            ? ' · ⚡ Fast'
            : '';
        final usageTotal = usageObject(data['displayTotal']);
        final header = DashboardCardHeader(
          glyph: AppGlyphs.resources,
          title: labels.codexLocalUsage,
          chevron: dashboard,
        );
        final modelLines = [
          if (model.isNotEmpty)
            Text(
              '$model · ${rate?.toStringAsFixed(2) ?? (idle ? '0.00' : '—')} tok/s$tier',
              maxLines: 1,
              overflow: TextOverflow.ellipsis,
              style: AppTextStyle.headline.of(context),
            ),
          Text(
            usage.unavailable
                ? labels.codexUnavailable
                : displayedModel == null
                ? (active > 0 ? labels.codexRunning(active) : labels.codexIdle)
                : _usageLine(displayedModel, context),
            maxLines: 1,
            overflow: TextOverflow.ellipsis,
            style: AppTextStyle.subheadline.of(context),
          ),
        ];
        if (!dashboard) {
          return DashboardCard(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              mainAxisSize: MainAxisSize.min,
              spacing: 6,
              children: [
                header,
                ...modelLines,
                Text(
                  '${labels.codexLocalQuota} · ${_usageLine(usageTotal, context, showTokens: false)}',
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis,
                  style: AppTextStyle.headline.of(context),
                ),
                Text(
                  '${labels.codexPlanReference}\n${labels.codexTierHint}',
                  style: AppTextStyle.caption1.of(context),
                ),
              ],
            ),
          );
        }

        final reportedPercent = usageTotal['percent'] as num?;
        final percent = reportedPercent?.isFinite == true
            ? reportedPercent
            : null;
        final fill = ((percent ?? 0) / 100).clamp(0.0, 1.0).toDouble();
        final density = DashboardDensityScope.of(context);
        final scale = DashboardDensityScope.scaleOf(context);
        final modelSummary = Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.start,
          spacing: 4,
          children: [
            ...modelLines,
            Text(
              labels.codexPlanReference,
              style: AppTextStyle.caption1.of(context),
            ),
            Text(
              labels.codexTierHint,
              style: AppTextStyle.headline.of(context),
            ),
          ],
        );
        final quota = Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.end,
          spacing: 2,
          children: [
            Text(
              labels.codexLocalQuota,
              textAlign: TextAlign.right,
              style: AppTextStyle.headline.of(context),
            ),
            Text(
              percent == null ? '—' : '${percent.toStringAsFixed(2)}%',
              style: AppTextStyle.display.of(context, emphasized: true),
            ),
            Text(
              _usageLine(
                usageTotal,
                context,
                showTokens: false,
                showPercent: false,
              ),
              textAlign: TextAlign.right,
              style: AppTextStyle.subheadline.of(context),
            ),
          ],
        );
        return DashboardCard(
          padding: EdgeInsets.zero,
          clipBehavior: Clip.antiAlias,
          onTap: () => ref
              .read(currentPageLabelProvider.notifier)
              .toPage(PageLabel.codexSpeed),
          child: Stack(
            children: [
              if (fill > 0)
                Positioned.fill(
                  child: FractionallySizedBox(
                    widthFactor: fill,
                    alignment: Alignment.centerLeft,
                    child: ColoredBox(
                      color: const Color(
                        defaultPrimaryColor,
                      ).withValues(alpha: 0.24),
                    ),
                  ),
                ),
              Padding(
                padding: dashboardCardPadding(density),
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  mainAxisSize: MainAxisSize.min,
                  spacing: 8,
                  children: [
                    header,
                    LayoutBuilder(
                      builder: (context, constraints) {
                        if (constraints.maxWidth < 540 * scale) {
                          return Column(
                            crossAxisAlignment: CrossAxisAlignment.stretch,
                            spacing: 12,
                            children: [modelSummary, quota],
                          );
                        }
                        return Row(
                          spacing: 20,
                          children: [
                            Expanded(flex: 2, child: modelSummary),
                            Expanded(child: quota),
                          ],
                        );
                      },
                    ),
                  ],
                ),
              ),
            ],
          ),
        );
      },
    );
  }
}

class CodexSpeedView extends ConsumerWidget {
  const CodexSpeedView({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final usage = ref.watch(codexUsageProvider);
    final labels = context.appLocalizations;
    return Scaffold(
      title: 'CodeX Speed',
      body: ListenableBuilder(
        listenable: usage,
        builder: (context, _) {
          final data = usage.data;
          final telemetry = usageObject(data['telemetry']);
          final activity = currentUsageActivity(data);
          final running = (activity['active'] as int?) ?? 0;
          final history = usageObject(data['activityStats']);
          final price = usageObject(data['pricing']);
          final date = _date(data['lastSync']);
          final hasControllerPrices = ((price['publication'] as int?) ?? 0) > 0;
          final month = usageObject(telemetry['traffic']);
          final device = data['name']?.toString();
          return ListView(
            padding: EdgeInsetsDirectional.fromSTEB(
              16,
              context.contentTopPadding,
              24,
              24,
            ),
            children: [
              const CodexUsageCard(dashboard: false),
              const SizedBox(height: 16),
              DashboardCard(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    DashboardCardHeader(
                      title: device?.isNotEmpty == true
                          ? device!
                          : labels.codexStatus,
                    ),
                    if (date.isNotEmpty) Text(labels.codexLastSync(date)),
                    if ((data['status'] as String?)?.isNotEmpty == true)
                      Text(data['status']! as String),
                    Text(
                      hasControllerPrices
                          ? labels.codexPricesUpdated
                          : labels.codexLocalPrices,
                    ),
                    if (price['publicationError'] != null)
                      Text(labels.codexPricePending),
                    Text(
                      running > 0
                          ? labels.codexRunning(running)
                          : labels.codexIdle,
                    ),
                    if (history.isNotEmpty)
                      Text(
                        labels.codexConcurrency(
                          (history['occurrences'] as int?) ?? 0,
                          ((history['seconds'] as int?) ?? 0) ~/ 60,
                          (history['peak'] as int?) ?? 0,
                        ),
                      ),
                    if (activity['incomplete'] == true ||
                        history['incomplete'] == true)
                      Text(labels.codexIncomplete),
                    Wrap(
                      crossAxisAlignment: WrapCrossAlignment.center,
                      spacing: 16,
                      children: [
                        _PeriodPicker(usage: usage),
                        Row(
                          mainAxisSize: MainAxisSize.min,
                          children: [
                            Checkbox(
                              value: usage.withCache,
                              onChanged: (cache) => usage.select(cache: cache),
                            ),
                            Text(labels.codexCache),
                          ],
                        ),
                      ],
                    ),
                  ],
                ),
              ),
              const SizedBox(height: 16),
              _UsageRows(
                title: labels.codexModels,
                rows: usageList(data['displayModels']),
              ),
              const SizedBox(height: 16),
              _UsageRows(
                title: labels.codexProjects,
                rows: usageList(data['displayProjects']),
              ),
              if (month.isNotEmpty) ...[
                const SizedBox(height: 16),
                DashboardCard(
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    spacing: 8,
                    children: [
                      DashboardCardHeader(
                        title: labels.codexTraffic('${month['month']}'),
                      ),
                      Text(_trafficLine(month)),
                      for (final group in usageList(month['groups']))
                        Text('${group['name']} · ${_trafficLine(group)}'),
                      Text(
                        labels.codexTrafficNote,
                        style: AppTextStyle.caption1.of(context),
                      ),
                    ],
                  ),
                ),
              ],
            ],
          );
        },
      ),
    );
  }
}

class _PeriodPicker extends StatelessWidget {
  const _PeriodPicker({required this.usage});

  final CodexUsage usage;

  @override
  Widget build(BuildContext context) {
    final labels = context.appLocalizations;
    final windows = usageList(usage.data['windows']);
    final selected = usage.data['window'];
    String label(UsageJson w) {
      final range = '${_date(w['start'])} – ${_date(w['end'])}';
      return w['current'] == true
          ? '${labels.codexCurrentPeriod} · $range'
          : range;
    }

    final current = windows.where((w) => w['id'] == selected).firstOrNull;
    return PopupAnchor(
      targetBuilder: (open) => PressRegion(
        shape: AppShape.md,
        onTap: open,
        child: Padding(
          padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
          child: Text(
            '${current == null ? labels.codexAllPeriods : label(current)} ▾',
          ),
        ),
      ),
      popupBuilder: (_) => PopupMenu(
        items: [
          for (final entry in windows)
            PopupMenuItem(
              label: label(entry),
              checked: entry['id'] == selected,
              onPressed: () => usage.select(window: entry['id']! as String),
            ),
          PopupMenuItem(
            label: labels.codexAllPeriods,
            checked: selected == 'all',
            onPressed: () => usage.select(window: 'all'),
          ),
        ],
      ),
    );
  }
}

class _UsageRows extends StatelessWidget {
  const _UsageRows({required this.title, required this.rows});

  final String title;
  final Iterable<UsageJson> rows;

  @override
  Widget build(BuildContext context) => DashboardCard(
    child: Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      spacing: 8,
      children: [
        DashboardCardHeader(title: title),
        if (rows.isEmpty) Text(context.appLocalizations.codexNoUsage),
        for (final row in rows)
          Wrap(
            spacing: 12,
            children: [
              Text('${row['name']}', style: AppTextStyle.headline.of(context)),
              Text(_usageLine(row, context)),
            ],
          ),
      ],
    ),
  );
}

String _usageLine(
  UsageJson row,
  BuildContext context, {
  bool showTokens = true,
  bool showPercent = true,
}) {
  final locale = Localizations.localeOf(context).toString();
  final usd = row['usd'] as num?;
  final percent = row['percent'] as num?;
  final unpriced = (row['unpriced'] as int?) ?? 0;
  return [
    if (showTokens && row['tokens'] is num)
      '${NumberFormat.decimalPattern(locale).format(row['tokens'])} Token',
    if (usd == null) '—' else '≈\$${usd.toStringAsFixed(2)}',
    if (showPercent && percent != null) '${percent.toStringAsFixed(2)}%',
    if (unpriced > 0) context.appLocalizations.codexUnpriced(unpriced),
  ].join(' · ');
}

String _date(Object? value) {
  final date = value is String ? DateTime.tryParse(value)?.toLocal() : null;
  if (date == null || date.year <= 1) return '';
  String twoDigits(int value) => '$value'.padLeft(2, '0');
  return '${twoDigits(date.month)}/${twoDigits(date.day)} '
      '${twoDigits(date.hour)}:${twoDigits(date.minute)}';
}

String _trafficLine(UsageJson row) {
  final up = (row['upload'] as int?) ?? 0;
  final down = (row['download'] as int?) ?? 0;
  return '↑ ${_bytes(up)} · ↓ ${_bytes(down)} · ${_bytes(up + down)}';
}

String _bytes(int value) {
  if (value < 1024) return '$value B';
  var amount = value / 1024;
  var unit = 0;
  const units = ['KB', 'MB', 'GB', 'TB'];
  while (amount >= 1024 && unit < units.length - 1) {
    amount /= 1024;
    unit++;
  }
  return '${amount.toStringAsFixed(2)} ${units[unit]}';
}
