---
title: "Command - devspace restart"
sidebar_label: devspace restart
---


Restarts containers where the sync restart helper is injected

## Synopsis


```
devspace restart [flags]
```

```
#######################################################
################## devspace restart ###################
#######################################################
Restarts containers where the sync restart helper
is injected:

devspace restart
devspace restart -n my-namespace
#######################################################
```


## Flags

```
  -c, --container string        Container name within pod to restart
  -h, --help                    help for restart
  -l, --label-selector string   Comma separated key=value selector list (e.g. release=test)
      --pick                    Select a pod (default true)
      --pod string              Pod to restart
```


## Global & Inherited Flags

```
      --config string         The devspace config file to use
      --debug                 Prints the stack trace if an error occurs
      --kube-context string   The kubernetes context to use
  -n, --namespace string      The kubernetes namespace to use
      --no-warn               If true does not show any warning when deploying into a different namespace or kube-context than before
  -p, --profile string        The devspace profile to use (if there is any)
      --profile-refresh       If true will pull and re-download profile parent sources
      --silent                Run in silent mode and prevents any devspace log output except panics & fatals
  -s, --switch-context        Switches and uses the last kube context and namespace that was used to deploy the DevSpace project
      --var strings           Variables to override during execution (e.g. --var=MYVAR=MYVALUE)
```

