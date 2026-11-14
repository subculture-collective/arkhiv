#!/usr/bin/env python3
"""Resolve the image plan for a public deployment target from each repository's main branch.

Frontend and API services deploy the image their repository published for its current
`main` commit. Platform cores are not part of any target: each consumer's GitHub
workflow rolls out the core it pins through the fleet-deploy receiver.
"""
import argparse
import json
import os
import re
import sys
import urllib.parse
import urllib.request

API = 'https://git.subcult.tv/api/v1'
REGISTRY = 'git.subcult.tv/subculture-collective'
TARGETS = {
    'platform': {
        'reference': {'repository': 'subculture-collective/arkhiv', 'image': 'archive-reference'},
        'democracy-now-reference': {'repository': 'subculture-collective/democracy-now-tv', 'image': 'democracy-now-tv-frontend'},
        'bill-nye-reference': {'repository': 'subculture-collective/bill-nye-tv', 'image': 'bill-nye-tv-frontend'},
        'riot-public-web': {'repository': 'subculture-collective/arkhiv-riot-grrrl', 'image': 'arkhiv-riot-grrrl-web'},
        'riot-public-api': {'repository': 'subculture-collective/arkhiv-riot-grrrl', 'image': 'arkhiv-riot-grrrl-api'},
    },
    'anarchist': {
        'public-web': {'repository': 'subculture-collective/arkhiv-anarchist', 'image': 'arkhiv-anarchist-web'},
        'public-api': {'repository': 'subculture-collective/arkhiv-anarchist', 'image': 'arkhiv-anarchist-api'},
    },
    'dedhed': {
        'web': {'repository': 'subculture-collective/dedhed', 'image': 'dedhed-web-public'},
        'api': {'repository': 'subculture-collective/dedhed', 'image': 'dedhed-api'},
        'operator': {'repository': 'subculture-collective/dedhed', 'image': 'dedhed-api'},
    },
}


def default_fetch(path):
    headers = {'User-Agent': 'arkhiv-deploy-plan/1'}
    token = os.environ.get('ARCHIVE_PACKAGE_READ_TOKEN')
    if token:
        headers['Authorization'] = 'token ' + token
    with urllib.request.urlopen(urllib.request.Request(API + path, headers=headers), timeout=30) as response:
        return json.loads(response.read())


def main_revision(fetch, repository, cache):
    if repository not in cache:
        sha = fetch('/repos/' + repository + '/branches/main')['commit']['id']
        if not re.fullmatch(r'[0-9a-f]{40}', sha):
            raise ValueError('Unexpected main revision for ' + repository)
        cache[repository] = sha
    return cache[repository]


def plan(target, fetch=default_fetch, source=None):
    if target not in TARGETS:
        raise ValueError('Unknown target ' + target)
    cache = {}
    services = {}
    for service, spec in TARGETS[target].items():
        revision = main_revision(fetch, spec['repository'], cache)
        services[service] = {'image': REGISTRY + '/' + spec['image'] + ':' + revision, 'revision': revision, 'from': spec['repository']}
    return {'target': target, 'services': services, 'source': source or {}}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--target', required=True, choices=sorted(TARGETS))
    parser.add_argument('--run-id', default='')
    args = parser.parse_args()
    print(json.dumps(plan(args.target, source={'run_id': args.run_id, 'planner': 'scripts/deploy_plan.py'}), indent=2))


if __name__ == '__main__':
    main()
