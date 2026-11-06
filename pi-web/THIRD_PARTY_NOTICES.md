# Third-party notices

Pi Web's own code is MIT licensed; see LICENSE. Dependencies keep their
respective licenses. The npm lockfile records exact versions and public
registry integrity digests. This notice accompanies the source and npm package.

## Optional native image dependencies

The optional `@img/sharp-libvips-*`, `@img/sharp-win32-*` and
`@img/sharp-wasm32` packages are installed separately from the public npm
registry for the user's platform. Their declared licenses include
LGPL-3.0-or-later, with Apache-2.0 and MIT for some combined distributions.
They are unmodified dependencies. Release approval requires the packed
member inventory to confirm whether any native dependency binaries are
included and to retain their license/source notices if distributed. Source and build instructions:

- https://github.com/lovell/sharp
- https://github.com/lovell/sharp-libvips
- https://github.com/libvips/libvips
- https://www.gnu.org/licenses/lgpl-3.0.html

## Development dependency jszip

`jszip` 3.10.1 is dual licensed under MIT OR GPL-3.0-or-later. This project
selects the MIT option. It is listed as a development dependency; the
published member inventory is checked separately. Its source and original license are at
https://github.com/Stuk/jszip/tree/v3.10.1.

## format 0.2.2 (demo dependency)

The registry metadata omits a license field. The published package's
Readme.md and format.js explicitly identify the MIT license and upstream
https://github.com/samsonjs/format. Copyright 2010–2014 Sami Samhuri
(sami@samhuri.net), as stated in Readme.md; format.js records 2010–2013.
The MIT text below applies to this component.

## khroma 2.1.0 (demo dependency)

The registry metadata omits a license field. The published package's
`license` file contains the MIT license. Copyright (c) 2019-present
Fabio Spampinato, Andrew Maney. Source:
https://github.com/fabiospampinato/khroma.

## MIT license text for the two demo components above

Permission is hereby granted, free of charge, to any person obtaining a
copy of this software and associated documentation files (the "Software"),
to deal in the Software without restriction, including without limitation
the rights to use, copy, modify, merge, publish, distribute, sublicense,
and/or sell copies of the Software, and to permit persons to whom the
Software is furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in
all copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL
THE AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING
FROM, OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER
DEALINGS IN THE SOFTWARE.
