import 'package:fl_clash/common/codex_usage.dart';
import 'package:fl_clash/ui/ui.dart';
import 'package:fl_clash/views/codex_speed.dart';
import 'package:fl_clash/views/dashboard/layout.dart';
import 'package:fl_clash/views/dashboard/widgets/card.dart';
import 'package:flutter/widgets.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

import '../helpers/test_app.dart';

void main() {
  testWidgets(
    'dashboard emphasizes the total quota and fills the card from the left',
    (tester) async {
      tester.view.devicePixelRatio = 1;
      tester.view.physicalSize = const Size(1100, 700);
      addTearDown(tester.view.resetPhysicalSize);
      addTearDown(tester.view.resetDevicePixelRatio);
      final usage = CodexUsage(() => null);
      addTearDown(usage.dispose);
      usage.data = {
        'speed': {'model': 'gpt-6.2-sol', 'state': 'idle'},
        'displayModels': [
          {'name': 'gpt-6.2-sol', 'tokens': 35792, 'usd': 0.1, 'percent': 0.01},
        ],
        'displayTotal': {'usd': 225, 'percent': 25, 'unpriced': 1},
      };
      await tester.pumpWidget(
        ProviderScope(
          overrides: [codexUsageProvider.overrideWithValue(usage)],
          child: const TestApp(
            locale: Locale('zh', 'CN'),
            child: Center(
              child: DashboardDensityScope(
                density: DashboardDensity.standard,
                scale: 1,
                child: SizedBox(
                  width: 1000,
                  height: 196,
                  child: CodexUsageCard(),
                ),
              ),
            ),
          ),
        ),
      );
      await tester.pumpAndSettle();

      final card = tester.getRect(find.byType(DashboardCard));
      final model = find.textContaining('gpt-6.2-sol');
      final quota = find.text('25.00%');
      final quotaLabel = find.text('本机使用额度');
      final quotaDetails = find.text('≈\$225.00 · 1 条未计价');
      final hint = find.textContaining('Fast是标准用量的2.5倍计费');
      expect(
        tester.getRect(quota).left,
        greaterThan(tester.getRect(model).right),
      );
      expect(tester.getRect(quota).right, tester.getRect(quotaLabel).right);
      expect(tester.getRect(quota).right, tester.getRect(quotaDetails).right);
      expect(
        tester.widget<Text>(quota).style!.fontSize,
        AppTextStyle.display.size,
      );
      expect(
        tester.widget<Text>(hint).style!.fontWeight,
        AppTextStyle.headline.weight,
      );
      expect(
        tester.widget<Text>(hint).style!.fontSize,
        AppTextStyle.headline.size,
      );
      final progress = tester.getRect(_progressColor());
      expect(progress.topLeft, card.topLeft);
      expect(progress.height, card.height);
      expect(progress.width, closeTo(card.width / 4, 0.01));
      expect(tester.takeException(), isNull);
    },
  );

  testWidgets('keeps the actual percentage when a narrow card fills to 100%', (
    tester,
  ) async {
    final usage = CodexUsage(() => null);
    addTearDown(usage.dispose);
    usage.data = {
      'speed': {'model': 'gpt-6.2-sol', 'state': 'idle'},
      'displayTotal': {'usd': 1125, 'percent': 125},
    };
    await tester.pumpWidget(
      ProviderScope(
        overrides: [codexUsageProvider.overrideWithValue(usage)],
        child: const TestApp(
          locale: Locale('zh', 'CN'),
          child: Center(child: SizedBox(width: 320, child: CodexUsageCard())),
        ),
      ),
    );
    await tester.pumpAndSettle();

    final card = tester.getRect(find.byType(DashboardCard));
    expect(tester.getRect(_progressColor()), card);
    final quota = tester.getRect(find.text('125.00%'));
    expect(quota.right, greaterThan(card.center.dx));
    expect(
      quota.top,
      greaterThan(tester.getRect(find.textContaining('Fast是标准用量')).bottom),
    );
    expect(tester.takeException(), isNull);
  });

  testWidgets('shows unknown quota without painting progress', (tester) async {
    final usage = CodexUsage(() => null);
    addTearDown(usage.dispose);
    usage.data = {
      'displayTotal': {'unpriced': 1},
    };
    await tester.pumpWidget(
      ProviderScope(
        overrides: [codexUsageProvider.overrideWithValue(usage)],
        child: const TestApp(
          locale: Locale('zh', 'CN'),
          child: Center(child: SizedBox(width: 320, child: CodexUsageCard())),
        ),
      ),
    );
    await tester.pumpAndSettle();

    expect(find.text('—'), findsOneWidget);
    expect(find.text('— · 1 条未计价'), findsOneWidget);
    expect(_progressColor(), findsNothing);
    expect(tester.takeException(), isNull);
  });

  testWidgets('shows all-model quota alongside the selected model estimate', (
    tester,
  ) async {
    final usage = CodexUsage(() => null);
    addTearDown(usage.dispose);
    usage.data = {
      'speed': {'model': 'gpt-6.2-sol', 'rate': 81.2, 'state': 'active'},
      'telemetry': {
        'activity': {
          'at': DateTime.now().toIso8601String(),
          'active': 2,
          'ultrafast': 1,
        },
      },
      'displayModels': [
        {'name': 'gpt-6.2-sol', 'tokens': 10240, 'usd': 6.3, 'percent': .7},
        {'name': 'gpt-6.2-astra', 'tokens': 1024, 'usd': 2.7, 'percent': .3},
      ],
      'displayTotal': {'usd': 9, 'percent': 1, 'unpriced': 2},
    };
    await tester.pumpWidget(
      ProviderScope(
        overrides: [codexUsageProvider.overrideWithValue(usage)],
        child: const TestApp(child: CodexUsageCard(dashboard: false)),
      ),
    );
    await tester.pumpAndSettle();
    expect(find.textContaining('≈\$6.30 · 0.70%'), findsOneWidget);
    expect(find.textContaining('≈\$9.00 · 1.00%'), findsOneWidget);
    expect(find.textContaining('⚡⚡ Ultrafast'), findsOneWidget);
    expect(tester.takeException(), isNull);
  });

  testWidgets('shows local prices without a sync placeholder', (tester) async {
    final usage = CodexUsage(() => null);
    addTearDown(usage.dispose);
    await tester.pumpWidget(
      ProviderScope(
        overrides: [codexUsageProvider.overrideWithValue(usage)],
        child: const TestApp(
          locale: Locale('zh', 'CN'),
          child: CodexSpeedView(),
        ),
      ),
    );
    await tester.pumpAndSettle();

    expect(find.text('本地价格表'), findsOneWidget);
    expect(find.text('尚未同步至 A 端'), findsNothing);
    expect(find.textContaining('等待 A 端价表'), findsNothing);
    expect(find.text('价格表已更新'), findsNothing);
  });

  testWidgets(
    'shows applied prices, a sync time and a billing cycle in Chinese',
    (tester) async {
      final usage = CodexUsage(() => null);
      addTearDown(usage.dispose);
      usage.data = {
        'name': 'Mac 4',
        'lastSync': '2026-11-19T13:12:00',
        'pricing': {'publication': 1795065120000},
        'window': 'current',
        'windows': [
          {
            'id': 'current',
            'start': '2026-11-19T01:00:00',
            'end': '2026-11-26T01:00:00',
            'current': true,
          },
        ],
      };
      await tester.pumpWidget(
        ProviderScope(
          overrides: [codexUsageProvider.overrideWithValue(usage)],
          child: const TestApp(
            locale: Locale('zh', 'CN'),
            child: CodexSpeedView(),
          ),
        ),
      );
      await tester.pumpAndSettle();
      expect(find.text('上次同步 11/19 13:12'), findsOneWidget);
      expect(find.text('价格表已更新'), findsOneWidget);
      expect(find.text('本地价格表'), findsNothing);
      expect(find.textContaining('本周期 · 11/19 01:00'), findsOneWidget);
      expect(tester.takeException(), isNull);
    },
  );
}

Finder _progressColor() => find.descendant(
  of: find.descendant(
    of: find.byType(DashboardCard),
    matching: find.byType(FractionallySizedBox),
  ),
  matching: find.byType(ColoredBox),
);
