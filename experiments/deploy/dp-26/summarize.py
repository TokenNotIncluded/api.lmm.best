#!/usr/bin/env python3
"""Validate every recorded run, then emit median/range tables. Never drop outliers."""
import argparse
import collections
import csv
import json
import math
import pathlib
import statistics

METRICS = ['success_per_second', 'cpu_seconds_per_success', 'allocated_bytes_per_success',
           'latency_p50_ms', 'latency_p95_ms', 'latency_p99_ms', 'ttfb_p99_ms',
           'mean_core_utilization', 'max_inflight', 'go_runtime_bytes_end', 'gc_cycles',
           'load_pss_peak_kib', 'load_rss_peak_kib', 'lifetime_pss_peak_kib',
           'observer_cpu_seconds', 'overlap_sum_pss_peak_kib', 'combined_cpu_seconds']

def stats(values):
    values = [v for v in values if v is not None]
    if not values:
        return None
    if any(not isinstance(v, (int, float)) or not math.isfinite(v) for v in values):
        raise ValueError('non-finite metric')
    return {'n': len(values), 'median': statistics.median(values), 'min': min(values), 'max': max(values), 'values': values}

def validate(records):
    ids = set()
    for record in records:
        if record['id'] in ids:
            raise ValueError('duplicate run ID')
        ids.add(record['id'])
        if record.get('aborted'):
            raise ValueError('aborted run must remain visible and cannot pass acceptance')
        for row in record['results']:
            result = row['result']
            if row['exit_code'] != 0 or not result:
                raise ValueError('missing/failed process result')
            if result['offered'] != result['success'] + result['failed'] + result['rejected']:
                raise ValueError('request conservation failed')
            if any(result[k] != 0 for k in ['failed', 'rejected', 'fixture_errors', 'inflight_at_end']):
                raise ValueError('correctness, queue or cleanup failure')
            if result['fixture_calls'] != result['success'] + result['warmup_calls']:
                raise ValueError('unexpected upstream calls')
            if result['max_inflight'] > 8 or result['mean_core_utilization'] > .75:
                raise ValueError('load budget exceeded')
            if not result['scope'].startswith('source-slice'):
                raise ValueError('unknown measurement scope')
        if record['source_slice'] is not True:
            raise ValueError('this summarizer is not a full-system acceptance gate')


def main():
    p = argparse.ArgumentParser()
    p.add_argument('--results', type=pathlib.Path, required=True)
    p.add_argument('--out', type=pathlib.Path, required=True)
    a = p.parse_args()
    records = [json.loads(line) for line in (a.results / 'runs.jsonl').read_text().splitlines()]
    validate(records)
    groups, flat = collections.defaultdict(list), []
    for record in records:
        group = record['id'].rsplit('-r', 1)[0]
        if len(record['results']) == 1:
            row = record['results'][0]
            result = row['result']
            metrics = {key: result.get(key) for key in METRICS}
            metrics.update(load_pss_peak_kib=row['memory_windows_kib']['load']['Pss'],
                           load_rss_peak_kib=row['memory_windows_kib']['load']['VmRSS'],
                           lifetime_pss_peak_kib=row['lifetime_peak_kib']['Pss'],
                           observer_cpu_seconds=record['observer_cpu_seconds'])
        else:
            metrics = {key: None for key in METRICS}
            metrics.update(overlap_sum_pss_peak_kib=record['overlap_sum_pss_peak_kib'],
                           combined_cpu_seconds=sum(row['result']['process_cpu_seconds'] for row in record['results']),
                           observer_cpu_seconds=record['observer_cpu_seconds'])
        metrics['run_id'] = record['id']
        groups[group].append(metrics)
        flat.append(dict(group=group, **metrics))
    for name, runs in groups.items():
        if len(runs) != 3:
            raise ValueError(f'{name}: expected all three repeats, got {len(runs)}')
    summary = {'scope': 'source-slice fixture only; full 1c1g/model/ledger/update acceptance NOT met',
               'groups': len(records), 'process_runs': sum(len(r['results']) for r in records),
               'successes': sum(row['result']['success'] for r in records for row in r['results']),
               'failures': 0, 'rejections': 0,
               'statistics': {name: {key: stats([r[key] for r in rows]) for key in METRICS} for name, rows in groups.items()}}
    a.out.mkdir(parents=True, exist_ok=True)
    (a.out / 'summary.json').write_text(json.dumps(summary, indent=2) + '\n')
    with (a.out / 'runs.csv').open('w', newline='') as f:
        writer = csv.DictWriter(f, fieldnames=['group', 'run_id'] + METRICS, lineterminator='\n')
        writer.writeheader(); writer.writerows(flat)
    rows = ['# 原始对照汇总', '', '每组均为三次运行。数值为中位数 [最小值, 最大值]。保留所有离群值。',
            '这些是源码组件与本地 TLS 测试服务、驱动合计，不是整站吞吐或容器资源。', '',
            '| 组 | 成功/秒 | CPU 毫秒/成功 | 分配 KiB/成功 | 延迟 p99 ms | 首字节 p99 ms | 运行 PSS 峰值 MiB |',
            '|---|---:|---:|---:|---:|---:|---:|']
    def fmt(name, key, scale=1):
        v = summary['statistics'][name][key]
        if not v: return '未测/不适用'
        return f"{v['median']*scale:.3f} [{v['min']*scale:.3f}, {v['max']*scale:.3f}]"
    for name in groups:
        if name.startswith('overlap-'): continue
        rows.append('| ' + name + ' | ' + ' | '.join(fmt(name, key, scale) for key, scale in [
            ('success_per_second', 1), ('cpu_seconds_per_success', 1000), ('allocated_bytes_per_success', 1/1024),
            ('latency_p99_ms', 1), ('ttfb_p99_ms', 1), ('load_pss_peak_kib', 1/1024)]) + ' |')
    rows += ['', '## 双进程重叠（不是发布、切流或结算验收）', '', '| 组 | 同时采样 PSS 合计峰值 MiB | 负载阶段 CPU 秒合计 |', '|---|---:|---:|']
    for name in groups:
        if name.startswith('overlap-'):
            rows.append(f"| {name} | {fmt(name, 'overlap_sum_pss_peak_kib', 1/1024)} | {fmt(name, 'combined_cpu_seconds')} |")
    rows += ['', '字段定义见 REPORT.md。不同阶段的峰值不能相加；PSS 合计为近同时采样值。',
             '中断记录在证据包 interruptions.jsonl；缺少完整采样的运行没有混入上述三次对照。', '']
    (a.out / 'TABLES.md').write_text('\n'.join(rows))
    print(json.dumps({k:v for k,v in summary.items() if k != 'statistics'}, ensure_ascii=False, indent=2))

if __name__ == '__main__': main()
