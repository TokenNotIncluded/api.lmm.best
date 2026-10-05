#!/usr/bin/env python3
"""Durable controller for a sealed financial maintenance episode.

This is separate from schema-only shared-PostgreSQL maintenance. Normal
deployment owners own capture, shutdown, installation, rollback and admission.
This controller owns the final business seal, rehearsal binding and one-shot
financial dispatch intent. It never edits their leases/state or restores a DB.
"""
import argparse
import fcntl
import hashlib
import importlib.util
import ipaddress
import json
import os
from pathlib import Path
import re
import shlex
import stat
import subprocess
import sys
import time
import urllib.parse
import urllib.error
import urllib.request

FORMAT = 'lmm-credit-financial-maintenance-v1'
HEX = re.compile(r'^[0-9a-f]{64}$')
NAME = re.compile(r'^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$')
NODE_OPERATIONS = ('guardian_start', 'capture', 'close', 'stop', 'prebridge_apply', 'prebridge_confirm', 'prebridge_stop',
                   'post_apply', 'post_confirm', 'maintenance_release', 'guardian_inspect',
                   'writer_inspect', 'publish_receipt', 'guardian_release',
                   'capture_status', 'prebridge_status', 'post_status', 'post_retry', 'seal_prebridge', 'seal_post')


class GateFailed(RuntimeError):
    pass


def require(condition, code):
    if not condition:
        raise GateFailed(code)


def unique(pairs):
    value = {}
    for key, item in pairs:
        require(key not in value, 'duplicate-json-key')
        value[key] = item
    return value


def decode(data):
    return json.loads(data, object_pairs_hook=unique)


def encode(value):
    return json.dumps(value, sort_keys=True, separators=(',', ':')).encode()


def digest(data):
    return hashlib.sha256(data).hexdigest()


def path(value):
    require(isinstance(value, str) and value.startswith('/') and
            str(Path(value)) == value and '..' not in Path(value).parts and
            re.fullmatch(r'/[A-Za-z0-9._/-]+', value), 'unsafe-path')
    return Path(value)


def read_bound(binding):
    require(set(binding) == {'path', 'sha256'} and HEX.fullmatch(binding['sha256']), 'artifact-binding')
    file = path(binding['path'])
    info = file.lstat()
    require(stat.S_ISREG(info.st_mode) and info.st_nlink == 1 and not info.st_mode & 0o022 and
            file.resolve() == file, 'unsafe-bound-file')
    data = file.read_bytes()
    require(digest(data) == binding['sha256'], 'bound-file-changed')
    return data


def write_once(file, data):
    descriptor = os.open(file, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
    with os.fdopen(descriptor, 'wb') as output:
        output.write(data)
        output.flush()
        os.fsync(output.fileno())
    directory = os.open(Path(file).parent, os.O_RDONLY | os.O_DIRECTORY)
    try:
        os.fsync(directory)
    finally:
        os.close(directory)


def atomic_state(file, value):
    # An interrupted checkpoint is preserved as evidence, rather than blocking
    # every future checkpoint or being guessed to be committed state.
    temporary = Path(str(file) + '.next.' + str(os.getpid()) + '.' + str(time.time_ns()))
    write_once(temporary, encode(value))
    os.replace(temporary, file)
    directory = os.open(Path(file).parent, os.O_RDONLY | os.O_DIRECTORY)
    try:
        os.fsync(directory)
    finally:
        os.close(directory)


def validate_plan(plan):
    require(plan.get('format') == FORMAT and NAME.fullmatch(plan.get('transition_id', '')) and
            HEX.fullmatch(plan.get('transition_intent_sha256', '')) and
            re.fullmatch(r'[0-9a-f]{40}', plan.get('source_sha', '')), 'controller-plan-identity')
    require(plan.get('confirmation') == 'api.lmm.best', 'controller-target-confirmation')
    require(digest(read_bound(plan['intent'])) == plan['transition_intent_sha256'], 'intent-binding')
    intent = decode(read_bound(plan['intent']))
    require(intent.get('format') == 'lmm-credit-transition-intent-v1' and intent.get('transition_id') == plan['transition_id'] and
            intent.get('source_sha') == plan['source_sha'] and intent.get('target_credits_per_usd') == 500000 and
            intent.get('provider_sha256') == plan['provider']['sha256'], 'intent-source-provider-binding')
    read_bound(plan['controller'])
    read_bound(plan['provider'])
    read_bound(plan['verifier'])
    read_bound(plan['generator'])
    read_bound(plan['fingerprint_generator'])
    for binding in plan['generator_helpers']:
        read_bound(binding)
    require(plan['controller']['sha256'] == digest(Path(__file__).read_bytes()), 'controller-source-changed')
    require(1 <= len(plan['nodes']) <= 16, 'complete-writer-nodes-required')
    names = []
    for node in plan['nodes']:
        require(NAME.fullmatch(node['name']) and NAME.fullmatch(node['ssh']) and
                NAME.fullmatch(node['hostname']), 'node-identity')
        require(node['deployment_tool'] in ('native', 'systemd') and node['service'] == 'lmm-api.service', 'normal-deployment-owner-required')
        require(NAME.fullmatch(node['guardian_unit']), 'guardian-systemd-unit-binding')
        require(node['writers'] == ['lmm-api.service'], 'supported-complete-writer-inventory-required')
        require(set(node['commands']) == set(NODE_OPERATIONS), 'normal-owner-operations-required')
        for operation in node['commands'].values():
            validate_operation(operation)
        if 'cleanup' in node:
            validate_operation(node['cleanup'])
        for binding in node['artifacts']:
            require(set(binding) == {'path', 'sha256'}, 'remote-artifact-binding')
            path(binding['path'])
            require(HEX.fullmatch(binding['sha256']), 'remote-artifact-digest')
        require(node['artifacts'] and node['probes'], 'node-artifact-and-origin-probes-required')
        if 'probe_resolve_address' in node:
            for probe in node['probes']:
                resolved_probe_operation(probe, node['probe_resolve_address'])
        for key in ('handoff', 'post_intent', 'prepare_config'):
            binding = node[key]
            require(binding in node['artifacts'], 'base-handoff-and-prepare-config-must-be-bound-artifacts')
        path(node['receipt_directory'])
        names.append(node['name'])
    require(len(names) == len(set(names)) and plan['database']['owner_node'] in names, 'writer-node-set')
    inventory_keys = ('name', 'ssh', 'hostname', 'deployment_tool', 'service', 'writers', 'guardian_unit')
    require(intent.get('writer_nodes') == [{key: node[key] for key in inventory_keys} for node in plan['nodes']] and
            intent.get('database') == plan['database'], 'intent-complete-writer-and-database-binding')
    database = plan['database']
    require(database['transport'] == 'local_peer' and NAME.fullmatch(database['os_user']) and
            NAME.fullmatch(database['peer_role']) and database['owner_role'] == 'lmm_api' and
            database['runtime_role'] == 'lmm_api' and NAME.fullmatch(database['database']) and
            NAME.fullmatch(database['schema']) and type(database['port']) is int and 1 <= database['port'] <= 65535,
            'database-peer-binding')
    path(database['socket_directory'])
    require(plan['public_probes'], 'public-barrier-required')
    for probe in plan['public_probes'] + [probe for node in plan['nodes'] for probe in node['probes']]:
        require(set(probe) == {'url', 'body_sha256'} and probe['url'].startswith(('https://', 'http://')) and
                HEX.fullmatch(probe['body_sha256']), 'barrier-probe-binding')
    clone = plan['clone']
    require(set(clone) == {'database', 'role', 'schema', 'port'} and
            all(NAME.fullmatch(clone[key]) for key in ('database', 'role', 'schema')) and
            clone['database'].startswith('credit_rebase_clone_') and clone['schema'] == database['schema'] and
            clone['role'] == database['owner_role'] == database['runtime_role'] and type(clone['port']) is int and 1024 <= clone['port'] <= 65535,
            'isolated-clone-binding')
    # Restoration and all DB verification/application are fixed controller
    # operations. A reviewed plan cannot redirect rehearsal SQL to production.
    regression = plan['regression']
    require(set(regression) == {'source_directory', 'source_sha', 'packages', 'run'} and
            regression['source_sha'] == plan['source_sha'], 'regression-source-binding')
    path(regression['source_directory'])
    require(regression['packages'] and all(re.fullmatch(r'\./[A-Za-z0-9_/-]+', value) for value in regression['packages']) and
            isinstance(regression['run'], str) and re.fullmatch(r'[A-Za-z0-9_|().^$]+', regression['run']), 'offline-regression-selection')
    require(set(plan['backup_commands']) == {'offhost_copy', 'offhost_verify'}, 'offhost-backup-operations-required')
    for operation in plan['backup_commands'].values():
        validate_operation(operation)
    return plan


def resolved_probe_operation(probe, address):
    require(isinstance(address, str), 'origin-probe-address-required')
    try:
        parsed_address = ipaddress.ip_address(address)
    except ValueError:
        raise GateFailed('origin-probe-address-invalid') from None
    require(str(parsed_address) == address and not (parsed_address.is_unspecified or
            parsed_address.is_loopback or parsed_address.is_multicast), 'origin-probe-address-invalid')
    url = urllib.parse.urlsplit(probe['url'])
    require(url.scheme == 'https' and url.hostname == 'api.lmm.best' and url.port in (None, 443) and
            url.username is None and url.password is None and not url.query and not url.fragment and
            url.path in ('/api/status', '/v1/models'), 'origin-probe-tls-url-required')
    resolved = '[' + address + ']' if parsed_address.version == 6 else address
    # Preserve the URL hostname for Host, SNI and certificate verification.
    # -q prevents a local curlrc from changing the reviewed probe behavior.
    return {'argv': ['/usr/bin/curl', '-q', '--noproxy', '*', '--silent', '--show-error', '--max-time', '10',
                     '--max-filesize', '8192', '--proto', '=https', '--max-redirs', '0',
                     '--resolve', 'api.lmm.best:443:' + resolved, '--write-out', '\n%{{http_code}}', probe['url']],
            'timeout_seconds': 15}


def validate_operation(operation):
    require(set(operation) == {'argv', 'timeout_seconds'} and isinstance(operation['argv'], list) and
            operation['argv'] and all(isinstance(arg, str) and '\x00' not in arg for arg in operation['argv']), 'operation-argv')
    path(operation['argv'][0])
    require(type(operation['timeout_seconds']) is int and 1 <= operation['timeout_seconds'] <= 3600, 'operation-timeout')


class Controller:
    def __init__(self, plan, plan_hash, work, executor=None):
        self.plan, self.plan_hash, self.work = plan, plan_hash, Path(work)
        self.executor = executor or subprocess.run
        self.state_path = self.work / 'state.json'
        self.operation_sequence = max((int(match.group(1)) for item in self.work.glob('*.log')
            if (match := re.match(r'^(\d+)-', item.name))), default=0)
        self.state = decode(self.state_path.read_bytes()) if self.state_path.exists() else {
            'format': FORMAT, 'plan_sha256': plan_hash, 'transition_id': plan['transition_id'],
            'transition_intent_sha256': plan['transition_intent_sha256'], 'phase': 'NEW', 'sequence': 0}
        require(self.state['plan_sha256'] == plan_hash, 'controller-plan-changed')

    def persist(self, phase, **values):
        self.state.update(values)
        self.state.update(phase=phase, sequence=self.state['sequence'] + 1, updated_at=time.time())
        atomic_state(self.state_path, self.state)

    def execute(self, label, operation, *, node=None, body=None, variables=None, cwd=None, env=None):
        argv = [arg.format_map(variables or {}) for arg in operation['argv']]
        if node:
            # SSH itself uses a remote shell. Quote each reviewed argument so
            # literal plan values are not interpreted as remote shell code.
            argv = ['/usr/bin/ssh', '-T', node['ssh'], shlex.join(argv)]
        self.operation_sequence += 1
        logfile = self.work / (f'{self.operation_sequence:06d}-' + label + '.log')
        require(not logfile.exists(), 'operation-evidence-already-exists')
        try:
            options = dict(input=body, capture_output=True, timeout=operation['timeout_seconds'])
            if cwd is not None:
                options['cwd'] = cwd
            if env is not None:
                options['env'] = env
            result = self.executor(argv, **options)
            write_once(logfile, result.stdout + result.stderr)
            require(result.returncode == 0, 'operation-failed:' + label)
            return result.stdout
        except subprocess.TimeoutExpired as error:
            write_once(logfile, (error.stdout or b'') + (error.stderr or b''))
            raise GateFailed('operation-outcome-unknown:' + label) from None

    def node_operation(self, node, operation, phase=None, *, body=None, variables=None):
        current = self.state.get('stopped_handoffs', {}).get(node['name'], node.get('handoff', {}))
        substitutions = {'handoff_path': current.get('path', ''), 'handoff_sha256': current.get('sha256', ''),
                         'base_handoff_path': node.get('handoff', {}).get('path', ''),
                         'base_handoff_sha256': node.get('handoff', {}).get('sha256', ''),
                         'post_intent_path': node.get('post_intent', {}).get('path', ''),
                         'post_intent_sha256': node.get('post_intent', {}).get('sha256', '')}
        substitutions.update(variables or {})
        output = self.execute(node['name'] + '-' + operation, node['commands'][operation], node=node, body=body, variables=substitutions)
        value = decode(output)
        if phase:
            require(value.get('phase') == phase, 'normal-owner-phase-mismatch:' + operation)
        require(value.get('transition_id') == self.plan['transition_id'] and
                value.get('transition_intent_sha256') == self.plan['transition_intent_sha256'], 'normal-owner-binding-mismatch')
        return value

    def verify_artifacts(self):
        read_bound(self.plan['controller'])
        read_bound(self.plan['provider'])
        read_bound(self.plan['intent'])
        read_bound(self.plan['generator'])
        read_bound(self.plan['verifier'])
        read_bound(self.plan['fingerprint_generator'])
        for binding in self.plan['generator_helpers']:
            read_bound(binding)
        for node in self.plan['nodes']:
            for index, artifact in enumerate(node['artifacts']):
                output = self.execute(node['name'] + '-artifact-' + str(index),
                    {'argv': ['/usr/bin/sha256sum', '--', artifact['path']], 'timeout_seconds': 30}, node=node)
                require(output.decode().split()[0] == artifact['sha256'], 'remote-artifact-changed')
            output = self.execute(node['name'] + '-hostname', {'argv': ['/usr/bin/python3', '-c', 'import socket; print(socket.gethostname())'], 'timeout_seconds': 30}, node=node)
            require(output.decode().strip() == node['hostname'], 'target-hostname-changed')

    def barriers(self):
        probes = [(None, index, probe) for index, probe in enumerate(self.plan['public_probes'])]
        probes += [(node, index, probe) for node in self.plan['nodes'] for index, probe in enumerate(node['probes'])]
        for node, index, probe in probes:
            if node is not None and 'probe_resolve_address' in node:
                output = self.execute(node['name'] + '-origin-barrier-' + str(index),
                    resolved_probe_operation(probe, node['probe_resolve_address']))
                body, separator, status = output.rpartition(b'\n')
                require(separator and status == b'503' and len(body) <= 8192 and
                        digest(body) == probe['body_sha256'], 'admission-is-not-closed')
                continue
            try:
                with urllib.request.urlopen(probe['url'], timeout=10) as response:
                    code, body = response.status, response.read(8193)
            except urllib.error.HTTPError as response:
                code, body = response.code, response.read(8193)
            require(code == 503 and len(body) <= 8192 and digest(body) == probe['body_sha256'], 'admission-is-not-closed')

    def guardians(self):
        generations = {}
        bindings = {}
        for node in self.plan['nodes']:
            value = self.node_operation(node, 'guardian_inspect')
            require(value.get('format') == 'lmm-credit-maintenance-guardian-v1' and
                    value.get('provider_sha256') == self.plan['provider']['sha256'] and
                    value.get('prepare_config_sha256') == node['prepare_config']['sha256'] and len(value.get('locks', [])) == 3 and
                    all(lock.get('held') is True and lock.get('inode', 0) > 0 for lock in value['locks']), 'guardian-three-lock-proof-missing')
            output = self.execute(node['name'] + '-guardian-unit', {'argv': ['/usr/bin/systemctl', 'show', node['guardian_unit'],
                '--property=MainPID,ActiveState,SubState,InvocationID'], 'timeout_seconds': 30}, node=node)
            unit = dict(line.split('=', 1) for line in output.decode().splitlines() if '=' in line)
            require(unit.get('ActiveState') == 'active' and unit.get('MainPID') == str(value['guardian_pid']), 'guardian-systemd-peer-generation-mismatch')
            require(re.fullmatch(r'[0-9a-f]{32}', unit.get('InvocationID', '')), 'guardian-invocation-identity-missing')
            generations[node['name']] = value['guardian_pid']
            bindings[node['name']] = {'pid': value['guardian_pid'], 'invocation_id': unit['InvocationID'],
                'locks': sorted([{key: lock[key] for key in ('path', 'device', 'inode')} for lock in value['locks']], key=lambda lock: lock['path'])}
        if 'guardian_lock_bindings' in self.state:
            require(bindings == self.state['guardian_lock_bindings'], 'guardian-or-lock-generation-changed-during-maintenance')
        else:
            # Capture starts only after this durable generation/three-inode
            # binding. Later inspections cannot silently replace the guard.
            self.persist(self.state['phase'], guardian_generations=generations, guardian_lock_bindings=bindings)

    def initial_guardians(self):
        deadline = time.monotonic() + 15
        while True:
            try:
                self.guardians()
                return
            except GateFailed:
                if 'guardian_lock_bindings' in self.state or time.monotonic() >= deadline:
                    raise
                # Root's preprovisioned simple unit may have started without
                # yet binding its socket. Only read-only proof is retried.
                time.sleep(0.5)

    def stopped(self):
        for node in self.plan['nodes']:
            value = self.node_operation(node, 'writer_inspect')
            require(value.get('format') == 'lmm-credit-maintenance-writer-v1' and value.get('stopped') is True and
                    node['writers'] == ['lmm-api.service'] and value.get('pid', 0) > 1 and
                    value.get('unit', {}).get('MainPID') == '0' and value.get('unit', {}).get('ActiveState') == 'inactive',
                    'complete-stopped-writer-proof-missing')

    def frozen_gates(self):
        self.verify_artifacts()
        self.barriers()
        self.guardians()
        self.stopped()
        # A new psql connection excludes only its own backend. Unknown active or
        # idle DB clients are still writers unless the sealed inventory proves
        # otherwise; never assume two stopped API units cover the whole DB.
        self.sql_bytes('database-client-drain', b"BEGIN READ ONLY; DO $guard$ BEGIN IF EXISTS (SELECT 1 FROM pg_catalog.pg_stat_activity WHERE datname=pg_catalog.current_database() AND backend_type='client backend' AND pid<>pg_catalog.pg_backend_pid()) THEN RAISE EXCEPTION 'unrecognized database client remains'; END IF; END $guard$; COMMIT;", as_owner=False)

    def publish_receipt(self, name, value, intent_phase=None):
        body = encode(value)
        file = self.work / name
        if file.exists():
            require(read_bound({'path': str(file), 'sha256': digest(body)}) == body, 'aggregate-receipt-changed')
        else:
            write_once(file, body)
        binding = {'path': str(file), 'sha256': digest(body)}
        if intent_phase:
            self.persist(intent_phase, global_release=binding)
        for node in self.plan['nodes']:
            destination = str(path(node['receipt_directory']) / name)
            receipt = self.node_operation(node, 'publish_receipt', body=body,
                variables={'receipt_path': destination, 'receipt_sha256': digest(body)})
            require(receipt.get('receipt_sha256') == digest(body), 'normal-owner-receipt-transport-mismatch')
        return binding

    def seal_stopped(self, stage):
        sealed = self.state.setdefault('stopped_handoffs', {})
        for node in self.plan['nodes']:
            destination = str(path(node['receipt_directory']) / (stage + '-stopped-handoff.json'))
            value = self.node_operation(node, 'seal_' + stage, variables={'handoff_output_path': destination})
            require(value.get('handoff_path') == destination and HEX.fullmatch(value.get('handoff_sha256', '')) and
                    value.get('provider_sha256') == self.plan['provider']['sha256'] and
                    value.get('prepare_config_sha256') == node['prepare_config']['sha256'], 'formal-stopped-handoff-binding-mismatch')
            sealed[node['name']] = {'path': destination, 'sha256': value['handoff_sha256']}
            # Each node's formal seal can be reconciled independently after
            # transport failure; do not lose successfully published evidence.
            self.persist(self.state['phase'], stopped_handoffs=sealed)

    def prepare(self):
        require(self.state['phase'] == 'NEW', 'prepare-must-not-replay')
        self.verify_artifacts()
        self.persist('CAPTURE_INTENT', capture_dispatched=False)
        for node in self.plan['nodes']:
            self.execute(node['name'] + '-guardian-start', node['commands']['guardian_start'], node=node)
        self.initial_guardians()
        self.persist('CAPTURE_INTENT', capture_dispatched=True)
        captures = []
        for node in self.plan['nodes']:
            captures.append(self.node_operation(node, 'capture', 'CAPTURED'))
        self.persist('CAPTURED', captures_sha256=digest(encode(captures)))
        self.persist('CLOSE_INTENT')
        for node in self.plan['nodes']:
            self.node_operation(node, 'close', 'ADMISSION_CLOSED')
        self.barriers()
        closed = self.publish_receipt('all-admission-closed.json', {'format': 'lmm-credit-all-admission-closed-v1',
            'transition_id': self.plan['transition_id'], 'transition_intent_sha256': self.plan['transition_intent_sha256'],
            'all_origins_closed': True, 'nodes': [node['name'] for node in self.plan['nodes']]})
        self.persist('ADMISSION_CLOSED', all_admission_closed=closed)
        self.guardians()
        self.persist('STOP_INTENT')
        for node in self.plan['nodes']:
            self.node_operation(node, 'stop', 'FROZEN', variables={
                'all_closed_path': str(path(node['receipt_directory']) / 'all-admission-closed.json'),
                'all_closed_sha256': closed['sha256']})
        self.seal_stopped('prebridge')
        self.frozen_gates()
        self.persist('FROZEN')
        self.persist('BRIDGE_INSTALL_INTENT')
        for node in self.plan['nodes']:
            self.node_operation(node, 'prebridge_apply')
            self.node_operation(node, 'prebridge_confirm', 'MAINTENANCE_CONFIRMED')
        self.barriers()
        self.guardians()
        self.persist('BRIDGES_INSTALLED')
        self.persist('BRIDGE_STOP_INTENT')
        for node in self.plan['nodes']:
            self.node_operation(node, 'prebridge_stop', 'FROZEN', variables={
                'all_closed_path': str(path(node['receipt_directory']) / 'all-admission-closed.json'),
                'all_closed_sha256': closed['sha256']})
        self.seal_stopped('post')
        self.frozen_gates()
        self.persist('FINAL_FROZEN')

    def backup(self):
        require(self.state['phase'] == 'FINAL_FROZEN', 'full-backup-requires-final-stopped-bridge-epoch')
        self.frozen_gates()
        database = self.plan['database']
        node = next(node for node in self.plan['nodes'] if node['name'] == database['owner_node'])
        argv = ['/usr/bin/runuser', '-u', database['os_user'], '--', '/usr/bin/env',
                'PGOPTIONS=-c search_path=' + database['schema'] + ',pg_catalog', '/usr/bin/pg_dump', '--format=custom', '--no-password',
                '-h', database['socket_directory'], '-p', str(database['port']), '-U', database['peer_role'], '-d', database['database']]
        # No --schema/--exclude/--no-owner flags: this is the complete database.
        archive = self.execute('full-database-backup', {'argv': argv, 'timeout_seconds': 3600}, node=node)
        require(archive.startswith(b'PGDMP'), 'full-backup-is-not-a-custom-archive')
        backup_path = self.work / 'final-full-database.dump'
        write_once(backup_path, archive)
        backup = {'path': str(backup_path), 'sha256': digest(archive)}
        self.persist('FULL_BACKUP_COPY_INTENT', backup=backup)
        self.finish_backup()

    def finish_backup(self):
        backup = self.state['backup']
        archive = read_bound(backup)
        require(archive.startswith(b'PGDMP'), 'full-backup-is-not-a-custom-archive')
        backup_path = path(backup['path'])
        self.frozen_gates()
        variables = {'backup_path': str(backup_path), 'backup_sha256': backup['sha256']}
        self.execute('offhost-backup-copy', self.plan['backup_commands']['offhost_copy'], body=archive, variables=variables)
        proof = decode(self.execute('offhost-backup-verify', self.plan['backup_commands']['offhost_verify'], variables=variables))
        require(proof.get('backup_sha256') == backup['sha256'] and proof.get('size_bytes') == len(archive), 'offhost-full-backup-proof-mismatch')
        self.frozen_gates()
        target = decode(self.sql_bytes('backup-database-identity', b"BEGIN READ ONLY; SELECT pg_catalog.json_build_object('database',pg_catalog.current_database(),'schema',pg_catalog.current_schema(),'system_identifier',(SELECT system_identifier::text FROM pg_catalog.pg_control_system()),'database_oid',(SELECT oid::text FROM pg_catalog.pg_database WHERE datname=pg_catalog.current_database()),'schema_oid',(SELECT oid::text FROM pg_catalog.pg_namespace WHERE nspname=pg_catalog.current_schema())); COMMIT;"))
        require(target['database'] == self.plan['database']['database'] and target['schema'] == self.plan['database']['schema'], 'full-backup-target-identity-mismatch')
        receipt_value = {'format': 'lmm-credit-financial-backup-v1', 'transition_id': self.plan['transition_id'],
            'transition_intent_sha256': self.plan['transition_intent_sha256'], 'provider_sha256': self.plan['provider']['sha256'],
            'source_sha': self.plan['source_sha'], 'target': target, 'full_database': True, 'archive_format': 'custom',
            'preserve_ownership': True, 'backup_sha256': backup['sha256'], 'size_bytes': len(archive),
            'frozen_guardian_bindings_sha256': digest(encode(self.state['guardian_lock_bindings']))}
        receipt = self.publish_receipt('full-financial-backup.receipt.json', receipt_value)
        self.persist('FULL_BACKUP_CREATED', backup=backup, backup_receipt=receipt, backup_target=target,
                     offhost_proof_sha256=digest(encode(proof)))
        # Preserve the exact final frozen backup epoch across local restoration
        # and late business sealing; later controller bookkeeping is separate.
        self.persist('FULL_BACKUP_CREATED', backup_frozen_state_sha256=digest(encode(self.state)))

    def local_environment(self):
        # Never inherit production DSNs, log DBs, redis, .env, cloud credentials
        # or preparation mode into a local rehearsal command.
        environment = {'PATH': '/usr/bin:/bin', 'LANG': 'C.UTF-8', 'GOMAXPROCS': '2',
                       'HOME': str(Path.home()), 'TMPDIR': str(self.work / 'local-tmp')}
        Path(environment['TMPDIR']).mkdir(mode=0o700, exist_ok=True)
        return environment

    def clone_paths(self):
        directory = self.work / 'local-clone'
        return directory, directory / 'data', directory / 'socket'

    def clone_command(self, label, argv, *, body=None, timeout=600):
        return self.execute(label, {'argv': argv, 'timeout_seconds': timeout}, body=body,
                            env=self.local_environment())

    def clone_sql(self, label, body, *, database=None, as_owner=True):
        _, _, socket = self.clone_paths()
        clone = self.plan['clone']
        argv = ['/usr/bin/psql', '-XqAt', '--no-password', '-v', 'ON_ERROR_STOP=1',
                '-h', str(socket), '-p', str(clone['port']), '-d', database or clone['database']]
        environment = self.local_environment()
        environment['PGOPTIONS'] = '-c search_path=' + clone['schema'] + ',pg_catalog'
        if as_owner:
            body = ('SET ROLE "'+clone['role']+'";\n').encode()+body
        return self.execute(label, {'argv': argv, 'timeout_seconds': 600}, body=body, env=environment)

    def clone_generation(self):
        _, data, socket = self.clone_paths()
        for directory in (data, socket):
            info = directory.lstat()
            require(stat.S_ISDIR(info.st_mode) and info.st_uid == os.geteuid() and
                    not info.st_mode & 0o077 and directory.resolve() == directory, 'clone-private-directory-changed')
        lines = (data / 'postmaster.pid').read_text().splitlines()
        require(len(lines) >= 6 and lines[1] == str(data) and lines[3] == str(self.plan['clone']['port']) and
                lines[4] == str(socket) and lines[5] == '', 'clone-postmaster-context-changed')
        generation = {'pid': int(lines[0]), 'started_at': lines[2], 'data_directory': str(data),
                      'socket_directory': str(socket), 'port': self.plan['clone']['port']}
        if 'clone_generation' in self.state:
            require(generation == self.state['clone_generation'], 'clone-postmaster-generation-changed')
        return generation

    def clone_identity(self):
        self.clone_generation()
        result = self.clone_sql('clone-identity', b"BEGIN READ ONLY; SELECT pg_catalog.json_build_object('database',pg_catalog.current_database(),'schema',pg_catalog.current_schema(),'system_identifier',(SELECT system_identifier::text FROM pg_catalog.pg_control_system()),'database_oid',(SELECT oid::text FROM pg_catalog.pg_database WHERE datname=pg_catalog.current_database()),'schema_oid',(SELECT oid::text FROM pg_catalog.pg_namespace WHERE nspname=pg_catalog.current_schema()),'local_socket',pg_catalog.inet_server_addr() IS NULL,'canonical_owner',(SELECT current_user='lmm_api' AND rolcanlogin AND rolinherit AND NOT rolsuper AND NOT rolcreatedb AND NOT rolcreaterole AND NOT rolbypassrls FROM pg_catalog.pg_roles WHERE rolname=current_user)); COMMIT;")
        identity = decode(result)
        require(identity.pop('local_socket') is True and identity.pop('canonical_owner') is True and identity['database'] == self.plan['clone']['database'] and
                identity['schema'] == self.plan['clone']['schema'], 'clone-is-not-isolated-local-target')
        if 'clone_identity' in self.state:
            require(identity == self.state['clone_identity'], 'clone-database-identity-changed')
        return identity

    def clone(self):
        require(self.state['phase'] == 'FULL_BACKUP_CREATED', 'clone-requires-final-full-backup')
        require(os.geteuid() != 0, 'local-clone-must-run-as-unprivileged-owner')
        self.frozen_gates()
        directory, data, socket = self.clone_paths()
        require(not directory.exists(), 'clone-must-be-new-no-existing-database-reuse')
        directory.mkdir(mode=0o700)
        socket.mkdir(mode=0o700)
        self.persist('CLONE_RESTORE_INTENT')
        try:
            self.clone_command('clone-initdb', ['/usr/bin/initdb', '-D', str(data), '--encoding=UTF8', '--locale=C.UTF-8', '--auth-local=trust', '--auth-host=reject'])
            options = '-c listen_addresses= -c shared_buffers=32MB -c max_connections=12 -k ' + str(socket) + ' -p ' + str(self.plan['clone']['port'])
            self.clone_command('clone-start', ['/usr/bin/pg_ctl', '-D', str(data), '-l', str(directory / 'server.log'), '-o', options, '-w', 'start'])
            generation = self.clone_generation()
            clone = self.plan['clone']
            identifier = lambda value: '"' + value.replace('"', '""') + '"'
            bootstrap = ('CREATE ROLE ' + identifier(clone['role']) + ' LOGIN INHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS;').encode()
            self.clone_sql('clone-create-owner-role', bootstrap, database='postgres', as_owner=False)
            self.clone_sql('clone-create-database', ('CREATE DATABASE ' + identifier(clone['database']) + ' OWNER ' + identifier(clone['role']) + ';').encode(), database='postgres', as_owner=False)
            self.clone_sql('clone-control-function-permission', ('GRANT EXECUTE ON FUNCTION pg_catalog.pg_control_system() TO '+identifier(clone['role'])+';').encode(), as_owner=False)
            self.clone_command('clone-full-restore', ['/usr/bin/pg_restore', '--exit-on-error', '--no-password', '-h', str(socket), '-p', str(clone['port']), '-d', clone['database']],
                               body=read_bound(self.state['backup']), timeout=3600)
            # The unfiltered complete archive is restored with its original
            # ownership. A complete table/count census records restoration,
            # without disclosing user or wallet rows.
            census_sql = b"BEGIN; CREATE TEMP TABLE restored_table_counts(name text,n bigint); DO $counts$ DECLARE t record; n bigint; BEGIN FOR t IN SELECT n.nspname,c.relname FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace WHERE c.relkind IN ('r','p') AND n.nspname NOT IN ('pg_catalog','information_schema') AND n.nspname NOT LIKE 'pg_toast%' AND n.nspname NOT LIKE 'pg_temp_%' LOOP EXECUTE pg_catalog.format('SELECT count(*) FROM %I.%I',t.nspname,t.relname) INTO n; INSERT INTO restored_table_counts VALUES(t.nspname||'.'||t.relname,n); END LOOP; END $counts$; SELECT pg_catalog.json_object_agg(name,n) FROM restored_table_counts; ROLLBACK;"
            census = decode(self.clone_sql('clone-complete-table-census', census_sql))
            identity = self.clone_identity()
            self.frozen_gates()
            self.persist('CLONE_RESTORED', clone_generation=generation, clone_identity=identity,
                         clone_table_count=len(census), clone_table_counts_sha256=digest(encode(census)))
        except BaseException:
            self.persist('CLONE_RESTORE_FAILED', recovery='local-clone-only-review-required-no-production-change')
            raise

    def verify_candidate(self, label, mode='verify'):
        self.clone_identity()
        clone = self.plan['clone']
        _, _, socket = self.clone_paths()
        cwd = self.work / 'clone-verify-cwd'
        cwd.mkdir(mode=0o700, exist_ok=True)
        require(not (cwd / '.env').exists(), 'clone-verify-environment-file-forbidden')
        environment = self.local_environment()
        environment['SQL_DSN'] = 'postgresql://'+urllib.parse.quote(clone['role'], safe='')+'@/'+clone['database']+'?'+urllib.parse.urlencode({
            'host': str(socket), 'port': clone['port'], 'sslmode': 'disable', 'search_path': clone['schema']})
        self.execute('clone-' + label + '-' + mode, {'argv': [self.plan['provider']['path'], 'migrate', '--'+mode], 'timeout_seconds': 600},
                     cwd=str(cwd), env=environment)

    def seal(self, business_seal):
        require(self.state['phase'] == 'CLONE_RESTORED', 'seal-requires-final-full-backup-and-real-clone')
        # Bridge preparation serves no business but must be stopped before the
        # final freeze snapshot, full backup, rehearsal and financial boundary.
        self.frozen_gates()
        seal = decode(read_bound(business_seal))
        require(seal.get('format') == 'lmm-credit-business-seal-v1' and
                seal.get('transition_id') == self.plan['transition_id'] and
                seal.get('transition_intent_sha256') == self.plan['transition_intent_sha256'], 'business-seal-identity')
        for key in ('source_snapshot', 'clone_snapshot', 'business_plan', 'production_sql', 'before_sql', 'after_sql', 'backup', 'clone_plan', 'clone_sql', 'clone_before_sql', 'clone_after_sql'):
            read_bound(seal[key])
        require(seal['backup'] == self.state['backup'], 'business-seal-backup-mismatch')
        production, clone = decode(read_bound(seal['business_plan'])), decode(read_bound(seal['clone_plan']))
        require(production['target'] == self.state['backup_target'], 'production-plan-does-not-target-full-backup-database')
        require(all(value.get('snapshot_state') == 'frozen_writers_stopped' and
                    value.get('production_apply_supported') == 'reviewed_postgres_sql_only' for value in (production, clone)),
                'financial-execution-requires-final-frozen-plan')
        require(clone['target'] == self.clone_identity(), 'clone-plan-does-not-target-owned-restored-cluster')
        for key, business in (('source_snapshot', production), ('clone_snapshot', clone)):
            snapshot = decode(read_bound(seal[key]))
            source_business = digest(encode({k: v for k, v in snapshot.items() if k != 'target'}))
            require(source_business == business['business_source_sha256'] and digest(encode(snapshot)) == business['source_sha256'], 'business-source-digest-mismatch')
            require(digest(encode({k: v for k, v in business.items() if k != 'plan_sha256'})) == business['plan_sha256'], 'target-plan-digest-mismatch')
            require(digest(encode({k: v for k, v in business.items() if k not in
                {'target', 'source_sha256', 'plan_sha256', 'business_plan_sha256'}})) == business['business_plan_sha256'], 'business-plan-digest-mismatch')
        require(production['business_plan_sha256'] == clone['business_plan_sha256'] == seal['business_plan_sha256'] and
                production['business_source_sha256'] == clone['business_source_sha256'] and
                production['target'] != clone['target'], 'clone-business-equivalence-mismatch')
        # Structural comparison proves target is the only input difference;
        # legacy source/plan hashes are target-specific and intentionally differ.
        ignored = {'target', 'source_sha256', 'plan_sha256'}
        require({k: v for k, v in production.items() if k not in ignored} ==
                {k: v for k, v in clone.items() if k not in ignored}, 'clone-plan-has-business-difference')
        require(seal.get('backup_frozen_state_sha256') == self.state['backup_frozen_state_sha256'], 'backup-is-not-bound-to-final-freeze')
        for plan_key, sql_key in (('business_plan', 'production_sql'), ('clone_plan', 'clone_sql')):
            rendered = self.execute('render-' + sql_key, {'argv': ['/usr/bin/python3', str(Path(__file__).resolve()), '_render-business-sql',
                '--generator', self.plan['generator']['path'], '--generator-sha256', self.plan['generator']['sha256'],
                '--plan', seal[plan_key]['path'], '--plan-sha256', seal[plan_key]['sha256']], 'timeout_seconds': 60})
            require(digest(rendered) == seal[sql_key]['sha256'], 'financial-sql-is-not-exact-reviewed-renderer-output')
        for prefix, plan_key in (('', 'business_plan'), ('clone_', 'clone_plan')):
            for mode in ('before', 'after'):
                rendered = self.execute('independent-verifier-' + prefix + mode, {'argv': ['/usr/bin/python3', self.plan['verifier']['path'],
                    '--plan', seal[plan_key]['path'], '--emit-postgres-verification-sql', mode], 'timeout_seconds': 60})
                require(digest(rendered) == seal[prefix + mode + '_sql']['sha256'], 'verification-sql-does-not-match-independent-verifier')
        self.verify_fingerprint_seal(seal)
        self.persist('SEALED', business_seal=business_seal, business_plan_sha256=seal['business_plan_sha256'])

    def verify_fingerprint_seal(self, seal):
        fingerprint = seal['original_fingerprint']
        inventory = read_bound(fingerprint['inventory'])
        read_bound(fingerprint['before_result'])
        require(decode(inventory).get('schema') == self.plan['database']['schema'], 'original-inventory-schema-mismatch')
        for prefix, plan_key in (('production', 'business_plan'), ('clone', 'clone_plan')):
            for mode in ('before', 'after'):
                key = prefix + '_' + mode
                sql_binding, receipt_binding = fingerprint[key+'_sql'], fingerprint[key+'_receipt']
                read_bound(sql_binding)
                receipt = decode(read_bound(receipt_binding))
                require(receipt.get('format') == 'lmm-credit-original-fingerprint-sql-v1' and receipt.get('stage') == mode and
                        receipt.get('generator_sha256') == self.plan['fingerprint_generator']['sha256'] and
                        receipt.get('inventory_sha256') == fingerprint['inventory']['sha256'] and
                        receipt.get('plan_sha256') == seal[plan_key]['sha256'] and
                        receipt.get('verifier_sha256') == self.plan['verifier']['sha256'] and
                        receipt.get('sql_sha256') == sql_binding['sha256'] and type(receipt.get('table_count')) is int and
                        receipt['table_count'] > 0, 'original-fingerprint-receipt-binding-mismatch')
                generation = 'generated-original-'+key+'-'+str(time.time_ns())
                sql_output, receipt_output = self.work/(generation+'.sql'), self.work/(generation+'.receipt.json')
                self.execute('render-original-'+key, {'argv': ['/usr/bin/python3', self.plan['fingerprint_generator']['path'], 'stage',
                    '--inventory', fingerprint['inventory']['path'], '--plan', seal[plan_key]['path'], '--stage', mode,
                    '--verifier', self.plan['verifier']['path'], '--output', str(sql_output), '--receipt', str(receipt_output)], 'timeout_seconds': 120})
                require(digest(sql_output.read_bytes()) == sql_binding['sha256'], 'original-fingerprint-sql-is-not-exact-reviewed-output')
                require(digest(receipt_output.read_bytes()) == receipt_binding['sha256'], 'original-fingerprint-receipt-is-not-exact-reviewed-output')
        result = self.sql('seal-original-before', fingerprint['production_before_sql'])
        require(digest(result) == fingerprint['before_result']['sha256'], 'full-original-tables-changed-before-seal')

    def fingerprint(self, seal, target, mode):
        bindings = seal['original_fingerprint']
        key = target+'_'+mode+'_sql'
        if target == 'clone':
            result = self.clone_sql('clone-original-'+mode, read_bound(bindings[key]))
        else:
            result = self.sql('production-original-'+mode, bindings[key])
        require(digest(result) == bindings['before_result']['sha256'], 'full-original-table-fingerprint-mismatch')
        file = self.work / (f'{self.operation_sequence:06d}-'+target+'-original-'+mode+'.tsv')
        write_once(file, result)
        return {'path': str(file), 'sha256': digest(result)}

    def business_seal(self):
        value = decode(read_bound(self.state['business_seal']))
        require(value['business_plan_sha256'] == self.state['business_plan_sha256'], 'business-seal-changed')
        return value

    def rehearse(self):
        require(self.state['phase'] == 'SEALED', 'rehearsal-must-not-replay')
        self.frozen_gates()
        seal = self.business_seal()
        require(decode(read_bound(seal['clone_plan']))['target'] == self.clone_identity(), 'clone-generation-changed')
        self.persist('REHEARSAL_INTENT')
        try:
            before_fingerprint = self.fingerprint(seal, 'clone', 'before')
            for label, key in (('before', 'clone_before_sql'), ('financial-dispatch', 'clone_sql'), ('after', 'clone_after_sql')):
                self.clone_sql('clone-' + label, read_bound(seal[key]))
            after_fingerprint = self.fingerprint(seal, 'clone', 'after')
            self.verify_candidate('candidate-schema', 'apply')
            self.clone_sql('clone-after-candidate-schema', read_bound(seal['clone_after_sql']))
            self.verify_candidate('candidate')
            self.verify_candidate('retained-bridge')
            after_fingerprint = self.fingerprint(seal, 'clone', 'after')
            regression = self.plan['regression']
            source = self.execute('regression-source', {'argv': ['/usr/bin/git', '-C', regression['source_directory'], 'rev-parse', 'HEAD'], 'timeout_seconds': 30})
            require(source.decode().strip() == regression['source_sha'], 'regression-source-changed')
            dirty = self.execute('regression-clean', {'argv': ['/usr/bin/git', '-C', regression['source_directory'], 'status', '--porcelain'], 'timeout_seconds': 30})
            require(not dirty.strip(), 'regression-source-dirty')
            self.execute('offline-financial-regression', {'argv': ['/usr/bin/go', 'test', '-p', '1', *regression['packages'], '-run', regression['run'], '-count=1'],
                'timeout_seconds': 3600}, cwd=regression['source_directory'], env=self.local_environment())
        except BaseException:
            self.persist('REHEARSAL_FAILED', recovery='clone-only-no-production-dispatch-review-required')
            raise
        self.frozen_gates()
        self.persist('REHEARSED', rehearsal_seal_sha256=self.state['business_seal']['sha256'],
                     clone_before_fingerprint=before_fingerprint, clone_after_fingerprint=after_fingerprint)

    def sql(self, label, binding):
        return self.sql_bytes(label, read_bound(binding))

    def sql_bytes(self, label, body, *, as_owner=True):
        database = self.plan['database']
        node = next(node for node in self.plan['nodes'] if node['name'] == database['owner_node'])
        argv = ['/usr/bin/runuser', '-u', database['os_user'], '--', '/usr/bin/env',
                'PGOPTIONS=-c search_path=' + database['schema'] + ',pg_catalog', '/usr/bin/psql', '-XqAt', '--no-password',
                '-v', 'ON_ERROR_STOP=1', '-h', database['socket_directory'], '-p', str(database['port']),
                '-U', database['peer_role'], '-d', database['database']]
        if as_owner:
            body = ('SET ROLE "'+database['owner_role']+'";\n').encode()+body
        return self.execute(label, {'argv': argv, 'timeout_seconds': 600}, node=node, body=body)

    def apply(self):
        require(self.state['phase'] == 'REHEARSED', 'financial-dispatch-cannot-be-replayed')
        self.frozen_gates()
        seal = self.business_seal()
        self.sql('production-before', seal['before_sql'])
        before_fingerprint = self.fingerprint(seal, 'production', 'before')
        intent = {'format': 'lmm-credit-financial-dispatch-v1', 'transition_id': self.plan['transition_id'],
                  'transition_intent_sha256': self.plan['transition_intent_sha256'],
                  'business_plan_sha256': self.state['business_plan_sha256'], 'business_seal_sha256': self.state['business_seal']['sha256'],
                  'production_sql_sha256': seal['production_sql']['sha256'], 'created_at': time.time()}
        write_once(self.work / 'financial-dispatch-intent.json', encode(intent))
        self.persist('DISPATCH_INTENT', dispatch_intent_sha256=digest(encode(intent)))
        try:
            self.sql('production-financial-dispatch', seal['production_sql'])
            self.sql('production-after', seal['after_sql'])
            after_fingerprint = self.fingerprint(seal, 'production', 'after')
            self.persist('APPLIED', production_before_fingerprint=before_fingerprint, production_after_fingerprint=after_fingerprint)
        except BaseException:
            self.persist('AMBIGUOUS', recovery='inspect-only-no-sql-retry-no-writer-start-no-restore')
            raise

    def inspect(self):
        require(self.state['phase'] in ('REHEARSED', 'DISPATCH_INTENT', 'AMBIGUOUS'), 'inspect-requires-existing-dispatch-intent')
        seal = self.business_seal()
        intent_file = self.work/'financial-dispatch-intent.json'
        intent_data = intent_file.read_bytes()
        intent = decode(read_bound({'path': str(intent_file), 'sha256': digest(intent_data)}))
        require(intent.get('format') == 'lmm-credit-financial-dispatch-v1' and intent.get('transition_id') == self.plan['transition_id'] and
                intent.get('transition_intent_sha256') == self.plan['transition_intent_sha256'] and
                intent.get('business_plan_sha256') == self.state['business_plan_sha256'] and
                intent.get('business_seal_sha256') == self.state['business_seal']['sha256'] and
                intent.get('production_sql_sha256') == seal['production_sql']['sha256'], 'durable-dispatch-intent-binding-mismatch')
        require(self.state.get('dispatch_intent_sha256', digest(intent_data)) == digest(intent_data), 'durable-dispatch-intent-changed')
        if self.state['phase'] == 'REHEARSED':
            self.persist('DISPATCH_INTENT', dispatch_intent_sha256=digest(intent_data), recovery='inspect-durable-intent-after-checkpoint-interruption')
        self.frozen_gates()
        try:
            self.sql('inspect-after', seal['after_sql'])
            after_fingerprint = self.fingerprint(seal, 'production', 'after')
        except GateFailed:
            try:
                self.sql('inspect-before', seal['before_sql'])
                self.fingerprint(seal, 'production', 'before')
                self.persist('NOT_APPLIED', recovery='review-required-no-automatic-retry')
            except GateFailed:
                self.persist('AMBIGUOUS', recovery='audit-or-values-disagree-repair-forward-under-guards')
            return
        self.persist('APPLIED', inspected_after_ambiguous_dispatch=True, production_after_fingerprint=after_fingerprint)

    def deploy(self):
        require(self.state['phase'] == 'APPLIED', 'post-deployment-requires-verified-financial-commit')
        self.frozen_gates()
        self.sql('pre-deploy-after', self.business_seal()['after_sql'])
        self.persist('POST_DEPLOYMENT_INTENT')
        self.finish_post_deployment(initial=True)

    def finish_post_deployment(self, initial=False):
        receipts = []
        for node in self.plan['nodes']:
            if initial:
                self.node_operation(node, 'post_apply')
                value = self.node_operation(node, 'post_confirm', 'MAINTENANCE_CONFIRMED')
            else:
                value = self.node_operation(node, 'post_status')
                phase = value.get('phase')
                if phase == 'MAINTENANCE_PREARM_FAILED':
                    self.node_operation(node, 'post_retry')
                    value = self.node_operation(node, 'post_confirm', 'MAINTENANCE_CONFIRMED')
                elif phase == 'NOT_DISPATCHED' and value.get('dispatch_verified_absent') is True:
                    self.node_operation(node, 'post_apply')
                    value = self.node_operation(node, 'post_confirm', 'MAINTENANCE_CONFIRMED')
                elif phase == 'AWAITING_CONFIRMATION':
                    value = self.node_operation(node, 'post_confirm', 'MAINTENANCE_CONFIRMED')
                else:
                    require(phase == 'MAINTENANCE_CONFIRMED', 'post-outcome-needs-normal-owner-repair-no-blind-replay')
            require(value.get('maintenance_stage') == 'post', 'post-owner-stage-mismatch')
            receipts.append(value)
        self.barriers()
        self.guardians()
        self.persist('ALL_NODES_CONFIRMED', confirmations_sha256=digest(encode(receipts)))

    def release(self):
        require(self.state['phase'] == 'ALL_NODES_CONFIRMED', 'release-requires-every-normal-owner-confirmation')
        self.barriers()
        self.guardians()
        fresh_confirmations = []
        for node in self.plan['nodes']:
            value = self.node_operation(node, 'post_status', 'MAINTENANCE_CONFIRMED')
            require(value.get('maintenance_stage') == 'post' and value.get('maintenance_confirmation') is True,
                    'every-current-owner-must-still-confirm-before-first-admission-release')
            fresh_confirmations.append(value)
        # Formal normal-owner confirmation performs fresh local strict health,
        # immutable installed payload and maintenance ingress probes. No node
        # is reopened until all have passed this current verification.
        for node in self.plan['nodes']:
            self.node_operation(node, 'post_confirm', 'MAINTENANCE_CONFIRMED')
        self.persist('ALL_NODES_CONFIRMED', confirmations_sha256=digest(encode(fresh_confirmations)))
        receipt = self.publish_receipt('all-nodes-confirmed.json', {'format': 'lmm-credit-maintenance-release-v1',
            'transition_id': self.plan['transition_id'], 'transition_intent_sha256': self.plan['transition_intent_sha256'],
            'business_plan_sha256': self.state['business_plan_sha256'], 'all_nodes_confirmed': True,
            'confirmations_sha256': self.state['confirmations_sha256'], 'nodes': [node['name'] for node in self.plan['nodes']]}, intent_phase='RELEASE_INTENT')
        self.finish_release(initial=True)

    def finish_release(self, initial=False):
        receipt = self.state['global_release']
        for node in self.plan['nodes']:
            if not initial:
                value = self.node_operation(node, 'post_status')
                require(value.get('maintenance_stage') == 'post' and value.get('phase') in ('CONFIRMED', 'MAINTENANCE_CONFIRMED'),
                        'release-outcome-needs-normal-owner-repair')
                if value['phase'] == 'CONFIRMED':
                    continue
            self.node_operation(node, 'maintenance_release', 'CONFIRMED', variables={
                    'global_confirmation_path': str(path(node['receipt_directory']) / 'all-nodes-confirmed.json'),
                    'global_confirmation_sha256': receipt['sha256']})
        # Historical payload cleanup remains a normal-owner command. A sealed
        # episode may request it here, after every admission release and while
        # the original three locks are still held. A default omitted cleanup
        # leaves payload review to the ordinary cleanup command.
        for node in self.plan['nodes']:
            if 'cleanup' in node:
                self.execute(node['name']+'-owner-cleanup', node['cleanup'], node=node,
                    variables={'handoff_path': self.state['stopped_handoffs'][node['name']]['path'],
                               'handoff_sha256': self.state['stopped_handoffs'][node['name']]['sha256'],
                               'backup_sha256': self.state['backup']['sha256'],
                               'financial_backup_receipt_path': str(path(node['receipt_directory'])/'full-financial-backup.receipt.json'),
                               'financial_backup_receipt_sha256': self.state['backup_receipt']['sha256']})
        for node in self.plan['nodes']:
            output = self.execute(node['name'] + '-guardian-before-release-unit', {'argv': ['/usr/bin/systemctl', 'show', node['guardian_unit'],
                '--property=MainPID,ActiveState,InvocationID'], 'timeout_seconds': 30}, node=node)
            unit = dict(line.split('=', 1) for line in output.decode().splitlines() if '=' in line)
            if unit.get('ActiveState') != 'inactive':
                require(unit.get('ActiveState') == 'active' and unit.get('MainPID') == str(self.state['guardian_generations'][node['name']]),
                        'guardian-release-generation-mismatch')
                require(unit.get('InvocationID') == self.state['guardian_lock_bindings'][node['name']]['invocation_id'],
                        'guardian-release-invocation-mismatch')
                self.execute(node['name'] + '-guardian-release', node['commands']['guardian_release'], node=node)
                output = self.execute(node['name'] + '-guardian-released-unit', {'argv': ['/usr/bin/systemctl', 'show', node['guardian_unit'],
                    '--property=MainPID,ActiveState'], 'timeout_seconds': 30}, node=node)
                unit = dict(line.split('=', 1) for line in output.decode().splitlines() if '=' in line)
            require(unit.get('MainPID') == '0' and unit.get('ActiveState') == 'inactive', 'normal-owner-guardian-release-unverified')
        self.persist('RELEASED', guardian_release='ordinary-owner-only-no-lock-path-deletion')

    def reconcile(self):
        """Observe owner states; never infer mutation failure from an SSH error."""
        phase = self.state['phase']
        owner_operation = ('post_status' if phase in ('POST_DEPLOYMENT_INTENT', 'RELEASE_INTENT') else
                           'prebridge_status' if phase in ('BRIDGE_INSTALL_INTENT', 'BRIDGE_STOP_INTENT') else
                           'capture_status')
        require(phase in ('CAPTURE_INTENT', 'CLOSE_INTENT', 'STOP_INTENT', 'BRIDGE_INSTALL_INTENT', 'BRIDGE_STOP_INTENT',
                          'POST_DEPLOYMENT_INTENT', 'RELEASE_INTENT'), 'no-normal-owner-intent-to-reconcile')
        observed = []
        for node in self.plan['nodes']:
            observed.append({'node': node['name'], 'owner': self.node_operation(node, owner_operation)})
        self.persist(phase, reconciled_owner_states=observed, recovery='explicit-resume-or-normal-owner-repair')
        phases = {item['owner'].get('phase') for item in observed}
        completed = {'CAPTURE_INTENT': ('CAPTURED', 'CAPTURED'), 'CLOSE_INTENT': ('ADMISSION_CLOSED', 'ADMISSION_CLOSED'),
                     'STOP_INTENT': ('FROZEN', 'FROZEN'), 'BRIDGE_INSTALL_INTENT': ('MAINTENANCE_CONFIRMED', 'BRIDGES_INSTALLED'),
                     'BRIDGE_STOP_INTENT': ('FROZEN', 'FINAL_FROZEN')}
        if phase in completed and phases == {completed[phase][0]}:
            if phase != 'CAPTURE_INTENT':
                self.barriers()
                self.guardians()
            if phase in ('STOP_INTENT', 'BRIDGE_STOP_INTENT'):
                self.seal_stopped('prebridge' if phase == 'STOP_INTENT' else 'post')
                self.frozen_gates()
            self.persist(completed[phase][1], recovery='explicit-resume')

    def resume(self):
        phase = self.state['phase']
        if phase == 'CAPTURE_INTENT' and self.state.get('capture_dispatched') is False:
            self.verify_artifacts()
            for node in self.plan['nodes']:
                self.execute(node['name']+'-guardian-start', node['commands']['guardian_start'], node=node)
            self.initial_guardians()
            self.persist('CAPTURE_INTENT', capture_dispatched=True)
            captures = [self.node_operation(node, 'capture', 'CAPTURED') for node in self.plan['nodes']]
            self.persist('CAPTURED', captures_sha256=digest(encode(captures)))
            self.continue_preparation()
        elif phase == 'FULL_BACKUP_COPY_INTENT':
            self.finish_backup()
        elif phase == 'POST_DEPLOYMENT_INTENT':
            self.verify_artifacts()
            self.barriers()
            self.guardians()
            # Confirmed candidates now run business jobs behind the barrier.
            # Financial before/after checks are no longer freeze checks here.
            self.finish_post_deployment()
        elif phase == 'RELEASE_INTENT':
            self.verify_artifacts()
            # Some nodes may already have reopened; the normal release owner
            # probes each remaining node. Do not require an all-503 barrier.
            receipt = self.state['global_release']
            self.publish_receipt('all-nodes-confirmed.json', decode(read_bound(receipt)))
            self.finish_release()
        elif phase in ('CAPTURE_INTENT', 'CLOSE_INTENT', 'STOP_INTENT', 'BRIDGE_INSTALL_INTENT', 'BRIDGE_STOP_INTENT'):
            self.reconcile()
            require(self.state['phase'] != phase, 'owner-repair-required-before-resume')
            self.continue_preparation()
        else:
            require(phase in ('CAPTURED', 'ADMISSION_CLOSED', 'FROZEN', 'BRIDGES_INSTALLED'), 'no-resumable-owner-phase')
            self.continue_preparation()

    def continue_preparation(self):
        # Resume only from phases established by independent read-only owner
        # reconciliation. An unresolved mutation is never submitted again.
        phase = self.state['phase']
        if phase == 'CAPTURED':
            self.guardians()
            self.persist('CLOSE_INTENT')
            for node in self.plan['nodes']:
                self.node_operation(node, 'close', 'ADMISSION_CLOSED')
            self.barriers()
            closed = self.publish_receipt('all-admission-closed.json', {'format': 'lmm-credit-all-admission-closed-v1',
                'transition_id': self.plan['transition_id'], 'transition_intent_sha256': self.plan['transition_intent_sha256'],
                'all_origins_closed': True, 'nodes': [node['name'] for node in self.plan['nodes']]})
            self.persist('ADMISSION_CLOSED', all_admission_closed=closed)
            phase = 'ADMISSION_CLOSED'
        if phase == 'ADMISSION_CLOSED':
            self.guardians()
            # A CLOSE_INTENT reconciled after transport failure may lack the
            # aggregate receipt. Publish only if its local immutable binding
            # was never persisted; each receipt transport is idempotent by hash.
            if 'all_admission_closed' not in self.state:
                closed = self.publish_receipt('all-admission-closed.json', {'format': 'lmm-credit-all-admission-closed-v1',
                    'transition_id': self.plan['transition_id'], 'transition_intent_sha256': self.plan['transition_intent_sha256'],
                    'all_origins_closed': True, 'nodes': [node['name'] for node in self.plan['nodes']]})
                self.persist('ADMISSION_CLOSED', all_admission_closed=closed)
            closed = self.state['all_admission_closed']
            self.persist('STOP_INTENT')
            for node in self.plan['nodes']:
                self.node_operation(node, 'stop', 'FROZEN', variables={
                    'all_closed_path': str(path(node['receipt_directory']) / 'all-admission-closed.json'), 'all_closed_sha256': closed['sha256']})
            self.seal_stopped('prebridge')
            self.frozen_gates()
            self.persist('FROZEN')
            phase = 'FROZEN'
        if phase == 'FROZEN':
            self.frozen_gates()
            self.persist('BRIDGE_INSTALL_INTENT')
            for node in self.plan['nodes']:
                self.node_operation(node, 'prebridge_apply')
                self.node_operation(node, 'prebridge_confirm', 'MAINTENANCE_CONFIRMED')
            self.barriers()
            self.guardians()
            self.persist('BRIDGES_INSTALLED')
            phase = 'BRIDGES_INSTALLED'
        if phase == 'BRIDGES_INSTALLED':
            self.barriers()
            self.guardians()
            self.persist('BRIDGE_STOP_INTENT')
            for node in self.plan['nodes']:
                self.node_operation(node, 'prebridge_stop', 'FROZEN', variables={
                    'all_closed_path': str(path(node['receipt_directory']) / 'all-admission-closed.json'),
                    'all_closed_sha256': self.state['all_admission_closed']['sha256']})
            self.seal_stopped('post')
            self.frozen_gates()
            self.persist('FINAL_FROZEN')


def publish_receipt_main(argv):
    parser = argparse.ArgumentParser(description='Private immutable aggregate receipt transport; does not edit normal owner state')
    parser.add_argument('--path', required=True)
    parser.add_argument('--sha256', required=True)
    args = parser.parse_args(argv)
    require(os.geteuid() == 0 and HEX.fullmatch(args.sha256), 'receipt-root-owner-required')
    target = path(args.path)
    for parent in target.parents:
        info = parent.lstat()
        require(stat.S_ISDIR(info.st_mode) and info.st_uid == 0 and not info.st_mode & 0o022 and
                not stat.S_ISLNK(info.st_mode), 'receipt-parent-unsafe')
    body = sys.stdin.buffer.read(65537)
    require(len(body) <= 65536 and digest(body) == args.sha256, 'receipt-transport-digest-mismatch')
    value = decode(body)
    require(value.get('format') in ('lmm-credit-all-admission-closed-v1', 'lmm-credit-maintenance-release-v1', 'lmm-credit-financial-backup-v1') and
            NAME.fullmatch(value.get('transition_id', '')) and HEX.fullmatch(value.get('transition_intent_sha256', '')), 'aggregate-receipt-format')
    if target.exists():
        require(read_bound({'path': str(target), 'sha256': args.sha256}) == body and target.lstat().st_uid == 0,
                'aggregate-receipt-already-exists-with-different-binding')
    else:
        write_once(target, body)
    print(json.dumps({'receipt_sha256': digest(body), 'transition_id': value['transition_id'],
                     'transition_intent_sha256': value['transition_intent_sha256']}))
    return 0


def render_sql_main(argv):
    parser = argparse.ArgumentParser(description='Private offline reviewed financial SQL renderer')
    parser.add_argument('--generator', required=True)
    parser.add_argument('--generator-sha256', required=True)
    parser.add_argument('--plan', required=True)
    parser.add_argument('--plan-sha256', required=True)
    args = parser.parse_args(argv)
    read_bound({'path': args.generator, 'sha256': args.generator_sha256})
    business = decode(read_bound({'path': args.plan, 'sha256': args.plan_sha256}))
    sys.path.insert(0, str(Path(args.generator).parent))
    sys.dont_write_bytecode = True
    spec = importlib.util.spec_from_file_location('sealed_credit_rebase_renderer', args.generator)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    # All sibling generator modules are bound and checked by the caller before
    # this private subprocess. This never opens a DB or starts a service.
    sys.stdout.write(module.postgres_sql(business))
    return 0


def main(argv=None):
    argv = list(sys.argv[1:] if argv is None else argv)
    if argv and argv[0] == '_publish-receipt':
        return publish_receipt_main(argv[1:])
    if argv and argv[0] == '_render-business-sql':
        return render_sql_main(argv[1:])
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('action', choices=('validate', 'prepare', 'backup', 'clone', 'seal', 'rehearse', 'apply', 'inspect', 'deploy', 'release', 'reconcile', 'resume', 'status'))
    parser.add_argument('--plan', required=True)
    parser.add_argument('--plan-sha256', required=True)
    parser.add_argument('--work', required=True)
    parser.add_argument('--confirm')
    parser.add_argument('--business-seal')
    parser.add_argument('--business-seal-sha256')
    args = parser.parse_args(argv)
    try:
        plan = validate_plan(decode(read_bound({'path': args.plan, 'sha256': args.plan_sha256})))
        work = path(args.work)
        if args.action == 'validate':
            print(json.dumps({'ok': True, 'transition_id': plan['transition_id'], 'writer_nodes': len(plan['nodes'])}))
            return 0
        require(args.action == 'status' or args.confirm == 'api.lmm.best', 'explicit-financial-action-confirmation-required')
        if not work.exists():
            require(args.action == 'prepare', 'controller-work-does-not-exist')
            work.mkdir(mode=0o700)
        info = work.lstat()
        require(stat.S_ISDIR(info.st_mode) and info.st_uid == os.geteuid() and stat.S_IMODE(info.st_mode) == 0o700 and work.resolve() == work, 'controller-work-unsafe')
        with (work / 'controller.lock').open('a') as lock:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
            controller = Controller(plan, args.plan_sha256, work)
            if args.action == 'seal':
                controller.seal({'path': args.business_seal, 'sha256': args.business_seal_sha256})
            elif args.action != 'status':
                getattr(controller, args.action)()
            print(json.dumps({key: controller.state.get(key) for key in ('phase', 'transition_id', 'transition_intent_sha256', 'business_plan_sha256', 'recovery')}))
        return 0
    except (OSError, ValueError, KeyError, GateFailed, BlockingIOError) as error:
        # Logs remain private. A summary must never include wallet values,
        # connection credentials or captured environment content.
        print(json.dumps({'ok': False, 'error_type': type(error).__name__, 'inspect_before_retry': True}), file=sys.stderr)
        return 1


if __name__ == '__main__':
    sys.exit(main())
