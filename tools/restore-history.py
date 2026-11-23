#!/usr/bin/env python3
"""Restore one original TClash artifact from local, encrypted Release assets."""

import argparse
import collections
import contextlib
import hashlib
import json
import os
import pathlib
import posixpath
import re
import shutil
import subprocess
import tarfile
import tempfile

INDEX_ASSET = 'TClash-history-index.tar.zst.gpg'
SUPPLEMENT_ASSET = 'TClash-history-supplement.tar.zst.gpg'
DIGEST = re.compile(r'[0-9a-f]{64}')
PACK = re.compile(r'TClash-history-[0-9]+\.tar\.zst\.gpg')


def pack_sources(path):
    if path.is_file():
        return [path]
    parts = sorted(path.parent.glob(path.name + '.part[0-9][0-9]'))
    if parts and parts == [path.with_name(f'{path.name}.part{i:02}') for i in range(1, len(parts)+1)]:
        return parts
    return []


@contextlib.contextmanager
def read_archive(path, key, home):
    parts = pack_sources(path)
    if not parts:
        raise ValueError(f'缺少加密分卷：{path.name}')
    gpg = ['gpg', '--no-options', '--homedir', str(home), '--batch', '--no-tty',
           '--quiet', '--pinentry-mode', 'loopback', '--no-symkey-cache',
           '--passphrase-file', str(key), '--decrypt']
    join = None
    if parts == [path]:
        decrypt = subprocess.Popen([*gpg, str(path)], stdout=subprocess.PIPE)
    else:
        join = subprocess.Popen(['cat', *map(str, parts)], stdout=subprocess.PIPE)
        decrypt = subprocess.Popen(gpg, stdin=join.stdout, stdout=subprocess.PIPE)
        join.stdout.close()
    decompress = subprocess.Popen(
        ['zstd', '-q', '-d', '-c'], stdin=decrypt.stdout, stdout=subprocess.PIPE,
    )
    decrypt.stdout.close()
    try:
        with tarfile.open(fileobj=decompress.stdout, mode='r|') as archive:
            yield archive
        while decompress.stdout.read(1024 * 1024):
            pass
        decompress_status = decompress.wait()
        decrypt_status = decrypt.wait()
        if decompress_status or decrypt_status or (join and join.wait()):
            raise ValueError(f'解密校验失败：{path.name}')
    finally:
        decompress.stdout.close()
        for process in (decompress, decrypt, join):
            if process is None:
                continue
            if process.poll() is None:
                process.terminate()
            process.wait()


def safe_relative(name):
    path = pathlib.PurePosixPath(name)
    if path.is_absolute() or '..' in path.parts or '\\' in name or '\0' in name:
        raise ValueError(f'无法还原路径：{name}')
    return path


def read_index(assets, key, home, index):
    with read_archive(assets / index, key, home) as archive:
        member = archive.next()
        if member is None or member.name != 'archive-manifest.json' or not member.isfile():
            raise ValueError('历史目录格式错误')
        if member.size > 64 * 1024 * 1024:
            raise ValueError('历史目录大小异常')
        with archive.extractfile(member) as stream:
            manifest = json.load(stream)
        if archive.next() is not None or manifest.get('schema') != 2:
            raise ValueError('历史目录版本不受支持')
        return manifest


def collect_chunks(assets, key, home, manifest, version, spool):
    by_pack = collections.defaultdict(set)
    for _relative, kind, _mode, digest, _size in version['entries']:
        if kind != 'file':
            continue
        for chunk in manifest['files'][digest]:
            pack = manifest['chunks'][chunk]
            if not DIGEST.fullmatch(chunk) or not PACK.fullmatch(pack):
                raise ValueError('历史目录中有无效的文件分卷')
            by_pack[pack].add(chunk)
    missing = [name for name in by_pack if not pack_sources(assets / name)]
    if missing:
        raise ValueError('缺少分卷 ' + ', '.join(missing))
    for pack, expected in sorted(by_pack.items()):
        with read_archive(assets / pack, key, home) as archive:
            for member in archive:
                if not member.name.startswith('objects/'):
                    continue
                digest = member.name[len('objects/'):]
                if digest not in expected:
                    continue
                if not member.isfile() or member.size > 2 * 1024 * 1024:
                    raise ValueError(f'分卷内容异常：{pack}')
                with archive.extractfile(member) as stream:
                    content = stream.read()
                if hashlib.sha256(content).hexdigest() != digest:
                    raise ValueError(f'分卷内容校验失败：{pack}')
                (spool / digest).write_bytes(content)
                expected.remove(digest)
            if expected:
                raise ValueError(f'分卷中缺少文件：{pack}')


def reconstruct(manifest, version, spool, output):
    label = safe_relative(version['label'])
    if len(label.parts) != 1:
        raise ValueError('制品名称格式错误')
    destination = output / label
    if destination.exists() or destination.is_symlink():
        raise ValueError(f'目标已存在：{destination}')
    with tempfile.TemporaryDirectory(prefix='.tclash-extract-', dir=output) as work:
        root = pathlib.Path(work) / label
        links = []
        directories = []
        for relative, kind, mode, digest, size in version['entries']:
            target = root / safe_relative(relative)
            if kind == 'dir':
                target.mkdir(parents=True, exist_ok=False)
                directories.append((target, mode))
            elif kind == 'link':
                parent = posixpath.dirname(relative)
                safe_relative(posixpath.normpath(posixpath.join(parent, digest)))
                links.append((target, digest))
            elif kind == 'file':
                hasher = hashlib.sha256()
                written = 0
                with target.open('xb') as stream:
                    for chunk in manifest['files'][digest]:
                        content = (spool / chunk).read_bytes()
                        hasher.update(content)
                        written += stream.write(content)
                if written != size or hasher.hexdigest() != digest:
                    raise ValueError(f'还原后校验失败：{relative or label}')
                target.chmod(mode & 0o777)
            else:
                raise ValueError('历史目录中有不受支持的文件类型')
        for target, link in links:
            target.symlink_to(link)
        for target, mode in reversed(directories):
            target.chmod(mode & 0o777)
        root.rename(destination)
    return destination


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--assets', required=True, type=pathlib.Path, help='保存加密附件的目录')
    parser.add_argument('--key', required=True, type=pathlib.Path, help='本机保存的恢复密钥文件')
    parser.add_argument(
        '--index', choices=(INDEX_ASSET, SUPPLEMENT_ASSET), default=INDEX_ASSET,
        help='选择主目录或补充目录的加密索引',
    )
    parser.add_argument('--version', help='历史制品编号，例如 V007')
    parser.add_argument('--output', type=pathlib.Path, help='还原到单独目录')
    args = parser.parse_args()
    if bool(args.version) != bool(args.output):
        parser.error('--version 和 --output 需要一起指定')
    for program in ('gpg', 'zstd'):
        if shutil.which(program) is None:
            parser.error(f'系统缺少 {program}')
    os.umask(0o077)
    try:
        with tempfile.TemporaryDirectory(prefix='tca-recover-', dir='/tmp') as home:
            manifest = read_index(args.assets, args.key, home, args.index)
            if not args.version:
                for version in manifest['versions']:
                    print(version['id'], version['origins'][0])
                return
            version = next((v for v in manifest['versions'] if v['id'] == args.version), None)
            if version is None:
                raise ValueError(f'没有此制品：{args.version}')
            args.output.mkdir(parents=True, exist_ok=True)
            with tempfile.TemporaryDirectory(prefix='.tclash-chunks-', dir=args.output) as temp:
                spool = pathlib.Path(temp)
                collect_chunks(args.assets, args.key, home, manifest, version, spool)
                print(reconstruct(manifest, version, spool, args.output))
    except (OSError, ValueError, KeyError, tarfile.TarError) as exc:
        parser.exit(1, f'{exc}\n')


if __name__ == '__main__':
    main()
