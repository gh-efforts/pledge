# pledge

A tool for automatically generating random CAR files and importing them into Boost for sealing tests.

## Configuration

```
export FULLNODE_API_INFO=xxx
export BOOST_API_INFO=xxx
```

## Usage:

1. pledge init
2. add funds to the pledge account
3. add funds to the Storage Market actor
4. pledge run (shares most of its parameters with the `boost offline-deal` command.)
