/*
 * Copyright 2023 The RuleGo Authors.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package config

// Agent defaults
const (
	// DefaultMaxStep The default maximum number of steps
	DefaultMaxStep = 50

	// MaxToolOutputLength tool outputs the maximum length
	MaxToolOutputLength = 50000

	// DefaultToolTimeoutSec Default tool timeout time (seconds)
	DefaultToolTimeoutSec = 120

	// DefaultMaxRetries defaults to the maximum number of retries by default
	DefaultMaxRetries = 3

	// MaxStreamChunks streams output the maximum number of chunks to prevent infinite loops
	MaxStreamChunks = 10000
)

// Model parameter defaults
const (
	// DefaultTemperature Sets the default temperature, avoiding output that is too random or too certain
	DefaultTemperature float32 = 0.7

	// DefaultTopP: Default TopP, maintaining diversity in outputs
	DefaultTopP float32 = 0.9

	// DefaultFrequencyPenalty Default frequency penalty to prevent duplicate content
	DefaultFrequencyPenalty float32 = 0.5

	// DefaultPresencePenalty Includes a penalty by default, encouraging discussion of new topics
	DefaultPresencePenalty float32 = 0.5
)
