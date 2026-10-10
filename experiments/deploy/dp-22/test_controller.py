"""Bounded local admission/controller regressions; no model or financial calls."""
from __future__ import annotations

import http.client
from http.server import ThreadingHTTPServer
import json
from pathlib import Path
import tempfile
import threading
import unittest

from controller import Instance, RolloutBlocked, Routes, watch_completions

TOKEN = 'dp22-isolated-controller-test-' + 'x' * 32


def seeded() -> Routes:
    routes = Routes(TOKEN, max_pending=2)
    routes.instances = {name: Instance(name, 18080 + i, 100 + i, Path('/tmp') / name)
                        for i, name in enumerate(('a', 'b'))}
    routes.current = 'a'
    return routes


def ticket(routes: Routes, value: str, request_id: str, name: str = 'a') -> None:
    routes.pending[value] = name
    routes.request_tickets[request_id] = value
    routes.ticket_requests[value] = request_id


class ControllerTests(unittest.TestCase):
    def test_cancel_revokes_only_unclaimed_selection_once(self):
        routes = seeded()
        ticket(routes, 'a' * 32, '1' * 32)
        calls = []
        self.assertFalse(routes.arm_drain('a', lambda: calls.append('drain')))
        self.assertTrue(routes.release_request('1' * 32))
        self.assertFalse(routes.ack('a', 'a' * 32))
        self.assertFalse(routes.release_request('1' * 32))
        self.assertEqual(calls, ['drain'])
        self.assertFalse(routes.pending)
        self.assertFalse(routes.request_tickets)
        self.assertFalse(routes.ticket_requests)

    def test_wrong_instance_and_duplicate_ack_do_not_reuse_ticket(self):
        routes = seeded()
        ticket(routes, 'a' * 32, '1' * 32)
        self.assertFalse(routes.ack('b', 'a' * 32))
        self.assertEqual(len(routes.pending), 1)
        self.assertTrue(routes.ack('a', 'a' * 32))
        self.assertFalse(routes.ack('a', 'a' * 32))

    def test_other_pending_request_still_blocks_drain(self):
        routes = seeded()
        ticket(routes, 'a' * 32, '1' * 32)
        ticket(routes, 'b' * 32, '2' * 32)
        calls = []
        routes.arm_drain('a', lambda: calls.append('drain'))
        routes.release_request('1' * 32)
        self.assertFalse(calls)
        self.assertEqual(routes.pending, {'b' * 32: 'a'})
        routes.ack('a', 'b' * 32)
        self.assertEqual(calls, ['drain'])

    def test_ack_racing_completion_signals_only_once(self):
        for _ in range(50):
            routes = seeded()
            ticket(routes, 'a' * 32, '1' * 32)
            calls = []
            routes.arm_drain('a', lambda: calls.append('drain'))
            barrier = threading.Barrier(3)
            results = []

            def run(action):
                barrier.wait(timeout=2)
                results.append(action())

            workers = [threading.Thread(target=run, args=(action,)) for action in
                       (lambda: routes.ack('a', 'a' * 32), lambda: routes.release_request('1' * 32))]
            for worker in workers:
                worker.start()
            barrier.wait(timeout=2)
            for worker in workers:
                worker.join(2)
                self.assertFalse(worker.is_alive())
            self.assertEqual(sorted(results), [False, True])
            self.assertEqual(calls, ['drain'])

    def test_withdrawal_cannot_race_reselection(self):
        routes = seeded()
        ticket(routes, 'a' * 32, '1' * 32)
        routes.commit('a', 'b')
        routes.arm_drain('a', lambda: None)
        with self.assertRaises(RolloutBlocked):
            routes.commit('b', 'a')
        self.assertEqual(routes.current, 'b')

    def test_selection_storage_has_a_hard_limit(self):
        routes = seeded()
        server = ThreadingHTTPServer(('127.0.0.1', 0), routes.handler())
        worker = threading.Thread(target=server.serve_forever, kwargs={'poll_interval': 0.01})
        worker.start()

        def select(request_id):
            connection = http.client.HTTPConnection('127.0.0.1', server.server_port, timeout=2)
            try:
                connection.request('GET', '/route', headers={
                    'X-Original-URI': '/transport', 'X-Dp22-Request-Id': request_id})
                response = connection.getresponse()
                response.read()
                return response.status
            finally:
                connection.close()

        try:
            self.assertEqual(select('1' * 32), 204)
            self.assertEqual(select('2' * 32), 204)
            self.assertEqual(select('3' * 32), 503)
            self.assertEqual(len(routes.pending), 2)
            self.assertEqual(len(routes.request_tickets), 2)
            routes.release_request('1' * 32)
            self.assertEqual(select('3' * 32), 204)
            self.assertEqual(select('3' * 32), 503)  # No second ticket for one request.
            self.assertEqual(len(routes.pending), 2)
        finally:
            server.shutdown()
            server.server_close()
            worker.join(2)

    def test_incomplete_or_invalid_completion_record_does_not_retire(self):
        routes = seeded()
        request_id = '1' * 32
        ticket(routes, 'a' * 32, request_id)
        retired = threading.Event()
        routes.arm_drain('a', retired.set)
        with tempfile.TemporaryDirectory(prefix='dp22-completion-') as directory:
            path = Path(directory) / 'completion.log'
            path.write_text('not JSON\n{"request_id":')
            stop = threading.Event()
            worker = threading.Thread(target=watch_completions, args=(path, routes, stop))
            worker.start()
            try:
                self.assertFalse(retired.wait(0.03))
                self.assertEqual(len(routes.pending), 1)
                with path.open('a') as output:
                    output.write(json.dumps(request_id) + ',"status":499}\n')
                    output.flush()
                self.assertTrue(retired.wait(2))
                self.assertFalse(routes.ack('a', 'a' * 32))
            finally:
                stop.set()
                worker.join(2)
                self.assertFalse(worker.is_alive())

    def test_missing_completion_source_does_not_expire_unknown_ticket(self):
        routes = seeded()
        ticket(routes, 'a' * 32, '1' * 32)
        retired = threading.Event()
        routes.arm_drain('a', retired.set)
        with tempfile.TemporaryDirectory(prefix='dp22-completion-') as directory:
            stop = threading.Event()
            worker = threading.Thread(target=watch_completions,
                args=(Path(directory) / 'missing.log', routes, stop))
            worker.start()
            try:
                self.assertFalse(retired.wait(0.03))
                self.assertEqual(len(routes.pending), 1)
            finally:
                stop.set()
                worker.join(2)


if __name__ == '__main__':
    unittest.main(verbosity=2)
