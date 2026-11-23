import 'package:code_assets/code_assets.dart';
import 'package:hooks/hooks.dart';
import 'package:setup_hooks/setup_hooks.dart';

void main(List<String> args) async {
  await build(args, (input, output) async {
    if (input.config.buildCodeAssets && input.config.code.targetOS == OS.windows) return;
    await const CoreBuilder().run(input: input, output: output);
  });
}
