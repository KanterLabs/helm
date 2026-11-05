"""Image command grammar shared with comments and structured progress."""


def add_image_args(parser, *, staged_ids=True):
    if staged_ids:
        parser.add_argument("--attachment-id", dest="attachment_ids", action="append", default=[], help="Existing staged attachment ID (repeat, maximum four total)")
    parser.add_argument('--image', dest='images', action='append', default=[], help='Local redacted PNG/JPEG/static WebP (repeat, maximum four)')
    parser.add_argument('--caption', dest='captions', action='append', default=[], help='One caption per image, in the same order')
    parser.add_argument('--commit-sha')
    parser.add_argument('--build')
    parser.add_argument('--viewport-width', type=int)
    parser.add_argument('--viewport-height', type=int)


def register(subparsers):
    attachments = subparsers.add_parser('attachments', help='Stage and inspect authenticated task images')
    actions = attachments.add_subparsers(dest='attachment_action', required=True)
    for name, handler in (('upload', cmd_attachments_upload), ('list', cmd_attachments_list),
                          ('status', cmd_attachments_status), ('download', cmd_attachments_download),
                          ('delete', cmd_attachments_delete)):
        command = actions.add_parser(name)
        command.add_argument('--task', required=True)
        if name in {'status', 'download', 'delete'}:
            command.add_argument('--attachment', required=True, help='Attachment ID')
        if name == 'upload':
            add_image_args(command, staged_ids=False)
        if name in {'upload', 'delete'}:
            command.add_argument('--operation-id', help='Stable operation ID for replay and recovery')
        if name == 'download':
            command.add_argument('--output', required=True, help='New explicit destination file; never binary stdout')
            command.add_argument('--thumbnail', action='store_true')
        command.set_defaults(handler=handler)
    comment = subparsers.add_parser('comment', help='Post an ordinary task comment with optional images')
    comment.add_argument('--task', required=True)
    comment.add_argument('--message', required=True)
    comment.add_argument('--operation-id')
    add_image_args(comment)
    comment.set_defaults(handler=cmd_comment)
