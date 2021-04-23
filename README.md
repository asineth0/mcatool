![](https://raw.githubusercontent.com/asineth0/mcatool/master/docs/icon.png)

# mcatool

CLI tool for packing & optimizing Minecraft saves.

## Features

* Pack/unpack Minecraft saves.
* Up to **35%** better compression ratio than existing algorithms.
* Purge chunks based on inhabited time.
* Multithreaded, will use all cores/threads by default.
* More yet to come.

## Performance

![](https://raw.githubusercontent.com/asineth0/mcatool/master/docs/graph.png)

* Seed: -5922982267743497625
* Pre-generated out 500 (square) for each dimension.
* Created w/ Paper & Chunkmaster.

## Notes

* **You may want to create a backup of your world first.**
* **Packed worlds are not normal ZIP files.**

## Usage

Pack/compress a world.

```sh
mcatool pack ./world world.zip
```

Unpack/decompress a world.

```sh
mcatool unpack world.zip ./world
```

Remove all unvisited chunks from a world.

```sh
mcatool purge ./world 0
```

Remove all chunks that haven't been inhabited for more than 1m.

```sh
mcatool purge ./world 60
```