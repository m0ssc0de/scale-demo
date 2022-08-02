# scale-demo

This project is used to debug issue blocks with `https://github.com/itering/scale.go`.
The way is minimal usage of `https://github.com/subquery/go-subql-substrate-dictionary`.

By testing, there are 3 blocks on Moonbase Alpha network have issue to decode. Not sure what went wrong so far.

## how to build static binary

Execute `docker build -t test .` . The binary will be placeed in the root(`/`) of image `test`

## how to run a develop env

By the instructions on `https://github.com/subquery/go-subql-substrate-dictionary/wiki`.

Or by the `nix`, just run `nix-shell` at the root of this project.

## Usage

`go-dictionary -c config.json -b 946237`

This demo just use the fields `rocksdb_config`, `chain_config` in `config.json`.

`-b 946237` is one block caouse the error/panic, `panic: Vec length 678189135 exceeds 50000 with subType Hash`. The other are `958672`, `958693` on Moonbase Alpha network.