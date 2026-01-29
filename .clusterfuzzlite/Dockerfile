# Copyright 2024 Google LLC
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#      http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

FROM gcr.io/oss-fuzz-base/base-builder-go:v1

# Install build dependencies
RUN apt-get update && apt-get install -y \
    build-essential \
    pkg-config \
    libgmp-dev \
    && rm -rf /var/lib/apt/lists/*

# Copy the project source code
COPY . $SRC/go-ethereum

WORKDIR $SRC/go-ethereum

# Copy build script
COPY ./.clusterfuzzlite/build.sh $SRC/
