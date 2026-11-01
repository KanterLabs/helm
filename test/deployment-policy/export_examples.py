#!/usr/bin/env python3
"""Export checksum-consistent synthetic contract examples, never real acceptance."""
import json
from pathlib import Path
import sys

from fixture import World, write_json

output = Path(sys.argv[1]).resolve()
output.mkdir(parents=True, exist_ok=False)
world = World(output)
error, evidence = world.run()
assert error is None, error
write_json(output / 'floor-before.json', world.floor)
write_json(output / 'floor-after.json', json.loads(world.envs['HELM_AUTHORIZATION_FLOOR']))
write_json(output / 'stop-request.json', world.observations[-1])
write_json(output / 'stop-response.json', world.observe(world.observations[-1]))
write_json(output / 'SYNTHETIC.json', {'synthetic_only': True,
    'bundle_sha256': world.settings['HELM_ACCEPTANCE_SHA256'],
    'warning': 'No real image or host is accepted. Temporary observer always exits 1.'})
print('Synthetic examples:', output)
