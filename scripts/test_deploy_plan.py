import unittest

from deploy_plan import REGISTRY, plan


class DeployPlanTests(unittest.TestCase):
    def fetch(self, path):
        if path.endswith('/branches/main'):
            repository = path.split('/repos/')[1].split('/branches')[0]
            return {'commit': {'id': ('1' if 'democracy' in repository else '2' if 'bill' in repository else '3') * 40}}
        raise AssertionError(path)

    def test_platform_plan_names_each_consumer_revision(self):
        result = plan('platform', self.fetch)
        services = result['services']
        self.assertEqual(services['democracy-now-reference']['image'], REGISTRY + '/democracy-now-tv-frontend:' + '1' * 40)
        self.assertEqual(services['bill-nye-reference']['image'], REGISTRY + '/bill-nye-tv-frontend:' + '2' * 40)
        self.assertEqual(services['reference']['from'], 'subculture-collective/arkhiv')
        self.assertEqual(services['riot-public-api']['revision'], '3' * 40)

    def test_no_target_deploys_a_platform_core(self):
        # Each consumer's GitHub workflow rolls out its core through fleet-deploy.
        for target in ('platform', 'anarchist', 'dedhed'):
            services = plan(target, self.fetch)['services']
            self.assertFalse({'archive-core', 'bill-core', 'democracy-core'} & set(services), target)
            self.assertFalse(any('archive-core' in entry['image'] for entry in services.values()), target)

    def test_dedhed_and_anarchist_targets(self):
        self.assertEqual(set(plan('dedhed', self.fetch)['services']), {'web', 'api', 'operator'})
        self.assertEqual(set(plan('anarchist', self.fetch)['services']), {'public-web', 'public-api'})
        with self.assertRaises(ValueError):
            plan('everything', self.fetch)


if __name__ == '__main__':
    unittest.main()
