#!/usr/bin/env bash
# Copyright 2026 Pasqal and its contributors
# SPDX-License-Identifier: Apache-2.0

docker exec ocs-master /bin/bash -lc \
  'source /opt/ocs/default/common/settings.sh && /tmp/adapter setup-qrmi-support --hosts ocs-master,ocs-worker1,ocs-worker2 --host-value EMU_FREE --queue all.q --prolog /tmp/qrmi-hooks/qrmi-ocs-prolog --epilog /tmp/qrmi-hooks/qrmi-ocs-epilog'

docker exec ocs-master /bin/bash -lc \
  'source /opt/ocs/default/common/settings.sh && qsub -b y -terse -l qpu=EMU_FREE /bin/echo QUICKINSTALL_QSUB_SMOKE'
