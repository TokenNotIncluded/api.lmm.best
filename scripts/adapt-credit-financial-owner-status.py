#!/usr/bin/env python3
"""Bound return-value adapter for the sealed 20261006 financial controller.

The original controller still owns every operation and checkpoint. Only a
successful native apply/confirm's bare return object is replaced by the same
sealed owner's immediately queried, persisted status. Neither JSON is edited.
"""
import argparse
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import stat
import sys
import types

ORIGINAL_PATH = Path('/home/lightjunction/.cache/api-release-preparation-20261006/final/source/scripts/run-credit-financial-maintenance.py')
ORIGINAL_SHA = '8e5a894f3ca806a1c9f86641e3ac96d2b0de310acc251d5f30a5439b285c611b'
PLAN_PATH = Path('/home/lightjunction/.cache/cf-20261006/financial-plan.bound.json')
PLAN_SHA = '095347319e48ce3e2400fb1031a611455ed1230b2ad80606e7e5cc7b702617ce'
WORK_PATH = Path('/home/lightjunction/.cache/cf-20261006')
SOURCE_SHA = '7af4bf9e56055a9a283b6545433e271a84b60026'
PROVIDER_SHA = '19dd6ff9d514cfea8338ba0c37b9a62d8ed1e10ddf77c7ba0f41d69a4eacdec9'
TRANSITION_ID = 'credit-financial-20261006'
INTENT_SHA = '60720d3b217167231ec10df4b95aab704366a05884c8b53e27dced40fb92bdcd'
VERSION = '0.2.82'
NATIVE_OPERATIONS = ('post_apply', 'post_confirm')


class AdapterFailed(RuntimeError):
    pass


def load_original(path=ORIGINAL_PATH, expected=ORIGINAL_SHA):
    """Compile precisely the hashed bytes, preserving the original sibling path.

    The parameters are isolated-test seams, never CLI options. Even a supplied
    path must contain the exact original seal, not an alternate expected hash.
    """
    path = Path(path)
    if expected != ORIGINAL_SHA or not path.is_absolute() or path.resolve() != path:
        raise AdapterFailed('sealed-original-path-or-digest')
    before = path.lstat()
    if not stat.S_ISREG(before.st_mode) or before.st_nlink != 1 or before.st_mode & 0o022:
        raise AdapterFailed('sealed-original-file-type-or-permissions')
    descriptor = os.open(path, os.O_RDONLY | os.O_NOFOLLOW | os.O_CLOEXEC)
    with os.fdopen(descriptor, 'rb') as file:
        info = os.fstat(file.fileno())
        if (info.st_dev, info.st_ino) != (before.st_dev, before.st_ino) or not stat.S_ISREG(info.st_mode) or info.st_nlink != 1 or info.st_mode & 0o022:
            raise AdapterFailed('sealed-original-file-type-or-permissions')
        raw = file.read(262145)
    if len(raw) > 262144 or hashlib.sha256(raw).hexdigest() != ORIGINAL_SHA:
        raise AdapterFailed('sealed-original-bytes-changed')
    name = '_lmm_sealed_financial_runner_8e5a894f'
    original = types.ModuleType(name)
    original.__file__ = str(path)
    original.__spec__ = importlib.util.spec_from_file_location(name, path)
    original.__sealed_sha256__ = ORIGINAL_SHA
    sys.modules[name] = original
    exec(compile(raw, str(path), 'exec'), original.__dict__)
    return original


def adapter_controller(original, action=None):
    if getattr(original, '__sealed_sha256__', None) != ORIGINAL_SHA:
        raise AdapterFailed('unsealed-original-module')

    class OwnerStatusController(original.Controller):
        def __init__(self, *args, **kwargs):
            super().__init__(*args, **kwargs)
            phases = {'deploy': ('APPLIED',), 'release': ('ALL_NODES_CONFIRMED',),
                      'resume': ('POST_DEPLOYMENT_INTENT', 'RELEASE_INTENT')}
            # original.main constructs this instance while holding its own
            # controller lock, closing the pre-entry phase observation race.
            if action is not None and action != 'status':
                original.require(self.state.get('phase') in phases[action], 'adapter-post-release-only')

        def node_operation(self, node, operation, phase=None, *, body=None, variables=None):
            if node['deployment_tool'] != 'native' or operation not in NATIVE_OPERATIONS:
                return super().node_operation(node, operation, phase, body=body, variables=variables)
            stage, action = operation.split('_', 1)
            expected_phase = 'AWAITING_CONFIRMATION' if action == 'apply' else 'MAINTENANCE_CONFIRMED'
            original.require(phase in (None, expected_phase) and body is None,
                             'adapter-native-call-shape')
            current = self.state.get('stopped_handoffs', {}).get(node['name'])
            original.remote_binding(current)
            substitutions = {
                'handoff_path': current['path'], 'handoff_sha256': current['sha256'],
                'base_handoff_path': node['handoff']['path'], 'base_handoff_sha256': node['handoff']['sha256'],
                'post_intent_path': node['post_intent']['path'], 'post_intent_sha256': node['post_intent']['sha256'],
            }
            original.require(all(key not in substitutions or value == substitutions[key]
                                 for key, value in (variables or {}).items()), 'adapter-handoff-override')
            substitutions.update(variables or {})
            status_operation = stage + '_status'
            command, status_command = node['commands'][operation], node['commands'][status_operation]
            workspace = original.flag_value(command['argv'], '--workspace')
            original.require(command['argv'][:4] == [status_command['argv'][0], 'operator', 'production', action] and
                             status_command['argv'][1:4] == ['operator', 'production', 'status'] and
                             original.flag_value(status_command['argv'], '--workspace') == workspace and
                             original.flag_value(command['argv'], '--maintenance-handoff') == '{handoff_path}' and
                             original.flag_value(command['argv'], '--maintenance-handoff-sha256') == '{handoff_sha256}' and
                             original.flag_value(status_command['argv'], '--maintenance-handoff') == '{handoff_path}' and
                             original.flag_value(status_command['argv'], '--maintenance-handoff-sha256') == '{handoff_sha256}',
                             'adapter-same-sealed-native-owner')
            # Original execute records success/failure/timeout bytes before any
            # decoding. Failure or an unknown outcome never queries status here.
            output = self.execute(node['name'] + '-' + operation, command, node=node,
                                  body=body, variables=substitutions)
            raw = original.decode(output)
            original.require(isinstance(raw, dict) and raw.get('phase') == expected_phase and
                             raw.get('version') == VERSION and not raw.get('failure'),
                             'adapter-native-raw-result')
            # Do not substitute invented bindings into raw output. Query only
            # the exact original status command, producing its own original log.
            value = super().node_operation(node, status_operation, expected_phase, variables=variables)
            expected = {
                'format': 2, 'phase': expected_phase, 'version': VERSION,
                'deployment_id': original.path(workspace).name,
                'maintenance_stage': stage, 'handoff_sha256': current['sha256'],
                'transition_id': self.plan['transition_id'],
                'transition_intent_sha256': self.plan['transition_intent_sha256'],
                'provider_sha256': self.plan['provider']['sha256'],
            }
            original.require(all(value.get(key) == item for key, item in expected.items()) and
                             type(value.get('format')) is int and
                             value.get('maintenance_confirmation', False) is (action == 'confirm') and
                             value.get('maintenance_admission_reopened', False) is False and
                             not value.get('failure'), 'adapter-persisted-owner-binding')
            # Native writeStatus takes its argument by value: legitimate raw
            # identity fields are empty, but any provided identity must agree.
            for key in ('deployment_id', 'maintenance_stage', 'handoff_sha256',
                        'transition_id', 'transition_intent_sha256', 'provider_sha256'):
                original.require(raw.get(key) in (None, '') or raw[key] == value[key],
                                 'adapter-raw-owner-binding:' + key)
            for key in ('maintenance_confirmation', 'maintenance_admission_reopened'):
                if key in raw:
                    original.require(type(raw[key]) is bool and raw[key] is value.get(key, False),
                                     'adapter-raw-owner-binding:' + key)
            if 'format' in raw:
                original.require(type(raw['format']) is int and raw['format'] in (0, 2), 'adapter-raw-format')
            return value

    return OwnerStatusController


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('action', choices=('deploy', 'release', 'resume', 'status'))
    parser.add_argument('--plan', default=str(PLAN_PATH))
    parser.add_argument('--plan-sha256', default=PLAN_SHA)
    parser.add_argument('--work', default=str(WORK_PATH))
    parser.add_argument('--confirm')
    args = parser.parse_args(argv)
    try:
        if (args.plan, args.plan_sha256, args.work) != (str(PLAN_PATH), PLAN_SHA, str(WORK_PATH)):
            raise AdapterFailed('fixed-episode-paths-required')
        if args.action != 'status' and args.confirm != 'api.lmm.best':
            raise AdapterFailed('explicit-financial-action-confirmation-required')
        original = load_original()
        plan = original.validate_plan(original.decode(original.read_bound({'path': args.plan, 'sha256': PLAN_SHA})))
        original.require(plan['controller'] == {'path': str(ORIGINAL_PATH), 'sha256': ORIGINAL_SHA} and
                         plan['source_sha'] == SOURCE_SHA and plan['provider']['sha256'] == PROVIDER_SHA and
                         plan['transition_id'] == TRANSITION_ID and plan['transition_intent_sha256'] == INTENT_SHA,
                         'adapter-fixed-episode-binding')
        state = original.decode((WORK_PATH / 'state.json').read_bytes())
        original.require(state.get('plan_sha256') == PLAN_SHA and state.get('transition_id') == TRANSITION_ID and
                         state.get('transition_intent_sha256') == INTENT_SHA, 'adapter-current-controller-binding')
        phases = {'deploy': ('APPLIED',), 'release': ('ALL_NODES_CONFIRMED',),
                  'resume': ('POST_DEPLOYMENT_INTENT', 'RELEASE_INTENT')}
        if args.action != 'status':
            original.require(state.get('phase') in phases[args.action], 'adapter-post-release-only')
        original.Controller = adapter_controller(original, args.action)
        forwarded = [args.action, '--plan', args.plan, '--plan-sha256', args.plan_sha256, '--work', args.work]
        if args.confirm is not None:
            forwarded += ['--confirm', args.confirm]
        return original.main(forwarded)
    except (AdapterFailed, OSError, ValueError, KeyError, RuntimeError):
        print(json.dumps({'ok': False, 'inspect_before_retry': True, 'adapter_failed': True}), file=sys.stderr)
        return 1


if __name__ == '__main__':
    sys.exit(main())
