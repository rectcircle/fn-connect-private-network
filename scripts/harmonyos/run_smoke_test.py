from unittest import TestCase, main
from run_smoke import parse_results


class ResultTests(TestCase):
    def test_ignores_previous_process_and_invalid_results(self):
        log = '\n'.join([
            '10-26 19:00:00 123 123 I HarmonySmoke: {"smoke":42}',
            '10-26 19:00:00 456 456 E HarmonySmoke: Probe failed',
            '10-26 19:00:00 456 456 I HarmonySmoke: {"workers":4}',
        ])
        self.assertEqual(parse_results(log, '456'), [{'workers': 4}])

    def test_does_not_match_partial_process_id(self):
        self.assertEqual(parse_results('10-26 time 1234 1234 I HarmonySmoke: {}', '123'), [])


if __name__ == '__main__':
    main()
