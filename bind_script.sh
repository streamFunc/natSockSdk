#!/bin/bash
directories=("./hole")  # 定义要绑定的目录列表
for dir in "${directories[@]}"; do
cd "$dir" || exit 1  # 进入目录
gomobile bind -target=android -o "${dir##*/}.aar"   # 执行 gomobile bind
cd - || exit 1     # 返回上级目录
done