package tui

import (
	"strings"

	"github.com/sakurai-ryo/innolens/internal/innodb"
)

// helpLines is the height of the ? panel. Entries are written to fit: the first
// line is the summary, the rest are the points worth looking at.
const helpLines = 3

// help is what the ? panel shows for a row. Keys are annotation field names,
// page types, redo record types and the datadir entry kinds.
var help = map[string]string{
	// datadir
	"table": `One file-per-table tablespace: the pages of one table and its indexes.
page 0 holds the FSP header, page 3 the SDI, the B+trees follow
innolens opens it read-only, so a running server is safe`,
	"#innodb_redo": `The redo log: what was changed, before it reached the .ibd files.
innolens reads from the last checkpoint to the end of the log
records address a page by space id and page number`,
	"schema": `A database. Each .ibd file under it is one table.
the directory name is the schema name, encoded when it is not ASCII
shared tablespaces are not in here; they sit next to the schemas`,

	// sections of the annotation tree
	"FIL header": `The 38 bytes every page starts with, whatever its type.
it identifies the page: space id, page number, type
the checksum and the LSN here are what a read checks first`,
	"FIL trailer": `The last 8 bytes: the checksum and the low half of the LSN, again.
written at the far end so a half-written page fails the compare
the doublewrite buffer is what repairs one`,
	"INDEX header": `The 36 bytes of B+tree bookkeeping after the FIL header.
how many records, where free space starts, which level of the tree
only INDEX and SDI pages have it`,
	"FSEG header": `The two file segment pointers of a B+tree: leaf pages and the rest.
only meaningful on the root page; other pages leave the bytes unused
each points at the INODE entry that owns the extents of that segment`,
	"FSP header": `Tablespace-wide bookkeeping, stored on page 0.
size, free limit, flags, and the lists of free and fragment extents
also where the SDI root page number lives`,
	"Records": `The user records in heap order, the order they were inserted.
key order is the next chain in each record header, not this list
infimum and supremum bracket that chain and hold no data`,
	"PAGE_FREE list": `Record space this page can reuse, whatever freed it.
purge puts a delete-marked record here; a split leaves the run it moved
PAGE_GARBAGE counts the bytes; an insert takes one before it grows the heap`,
	"Page directory": `Sparse index of the record chain, growing back from the FIL trailer.
one slot per 4 to 8 records, so a lookup binary-searches the page
PAGE_N_DIR_SLOTS counts the slots`,
	"SDI header": `Header of one SDI record: the type and id of the object it describes.
type 1 is a table, type 2 a tablespace
the JSON that follows it is zlib-compressed`,
	"SDI JSON": `The table definition, decompressed. innolens reads columns from this.
column order, types and lengths all come from here
without it a record is just bytes; redo carries a cut-down copy`,
	"page usage": `Where the 16 KB of this page goes.
records, garbage and free always fill the middle between the headers
a page low in records and high in garbage is waiting for purge`,
	"replay": `The redo records of this page, applied one at a time with n and p.
each step writes what the record says and moves FIL_PAGE_LSN forward
a record the page has already seen is skipped, as recovery skips it`,
	"clustered index": `The primary key this record stores, which is where the row itself is.
a secondary index leaf holds its key columns and the PK, nothing else
enter looks the PK up in the clustered index, the read a query makes next`,
	"versions": `Older versions of this row, rebuilt by following DB_ROLL_PTR.
each step reads one undo record and puts its old column values back
v sets a read view: the newest version it counts as committed is marked`,
	"row version": `One version of the row as some transaction left it.
trx_id is the transaction that wrote it, DB_ROLL_PTR the version before
enter opens the undo record that holds it`,
	"no earlier version": `The chain ends at an insert: the row did not exist before it.
insert undo only records the key, enough to remove the row on rollback
it is discarded at commit, so an older chain may just be gone`,
	"CREATE TABLE": `The table as SHOW CREATE TABLE would print it, rebuilt from the SDI.
the SDI is the data dictionary copy stored in the tablespace itself
the AUTO_INCREMENT counter is not in it, and partitioning is only noted`,
	"lock statement": `One choice of the l picker: the statement is put together step by step.
isolation level, then the statement, the index to scan, the comparison
only the key values are typed; esc goes back one step`,
	"locks": `Record locks the statement given to l takes, worked out from the tree.
one per index record the scan reads, plus the row behind a secondary entry
none of this is on disk: a server keeps its locks in memory, per transaction`,
	"lock": `One record lock, spelled as performance_schema.data_locks would.
X or S alone is next-key, the record and the gap before it; ,GAP is the gap
,REC_NOT_GAP the record only; on the supremum, the gap after the last record`,
	"find index": `One B+tree of the table: enter picks it, then the key is typed.
the lookup narrows on the first column of the index only`,
	"insert index": `One B+tree of the table: enter picks it, then the key is typed.
the insert is placed by the first column of the index only
the other columns are not typed: the record is sized like its neighbour`,
	"insert": `What an INSERT of the key does to the leaf page, worked out from the tree.
where it goes, what it takes the bytes from, what the header records
none of this is written: the pages stay as they are on disk`,
	"insert split": `The page cannot take the record: half of it moves to a new page.
an ascending run is cut two records past the new one, a descending one ahead
otherwise at the middle; the parent gets a node pointer for the upper half`,
	"descent": `One page of a key lookup, root first, one page per level of the tree.
a node page picks the last child whose key does not sort after the key
the leaf row is where the record is, or where it would be inserted`,
	"redo": `Redo records that modified this page, from the last checkpoint on.
anything already checkpointed away is no longer in the log
expand one to see the bytes it wrote`,

	// FIL header
	"FIL_PAGE_SPACE_OR_CHKSUM": `Checksum of the page, crc32c by default.
the name is historic: it held the space id before 4.0
it is compared with FIL_PAGE_END_CHKSUM in the trailer on every read`,
	"FIL_PAGE_OFFSET": `Page number within the tablespace.
page 0 starts at byte 0, page 1 at 16384, and so on
a page whose number does not match where it sits was copied there`,
	"FIL_PAGE_PREV": `Previous page on this level of the B+tree, or FIL_NULL.
each level is a doubly linked list; a range scan walks it
pages outside a tree leave it FIL_NULL`,
	"FIL_PAGE_NEXT": `Next page on this level of the B+tree, or FIL_NULL.
with PREV it keeps every level linked in key order
the last page of a level ends the chain with FIL_NULL`,
	"FIL_PAGE_LSN": `Last redo LSN applied to this page.
the FIL trailer repeats its low 4 bytes; a mismatch means a torn write
recovery skips redo records older than this`,
	"FIL_PAGE_TYPE": `What the bytes after this header mean.
INDEX pages hold rows, the rest is allocation and metadata
a page is typed when it is first used, not when it is allocated`,
	"FIL_PAGE_FILE_FLUSH_LSN": `Only used on page 0 of the system tablespace; zero everywhere else.
there it records how far the log was flushed at a clean shutdown
file-per-table spaces never write it`,
	"FIL_PAGE_SPACE_ID": `The tablespace this page belongs to.
it matches FSP_SPACE_ID on page 0 and the .ibd the page came from
a redo record addresses a page by this id plus the page number`,

	// FIL trailer
	"FIL_PAGE_END_CHKSUM": `Second copy of the checksum, at the far end of the page.
a torn 16 KB write leaves the two copies disagreeing
older versions wrote a different algorithm here, so it is not always equal`,
	"FIL_PAGE_END_LSN_LOW32": `Low 32 bits of FIL_PAGE_LSN, repeated at the end of the page.
head and tail must match, or the page is half old and half new
that is the damage the doublewrite buffer exists to undo`,

	// INDEX header
	"PAGE_N_DIR_SLOTS": `Number of slots in the page directory.
the directory grows backwards from the trailer, 2 bytes per slot
each slot owns 4 to 8 records; n_owned in a record header says how many`,
	"PAGE_HEAP_TOP": `Offset where the free space starts.
records are cut from here upwards as they are inserted
everything from here to the page directory is unused`,
	"PAGE_N_HEAP": `Records ever placed in the heap, deleted ones included.
the top bit is the format flag: set means compact
infimum and supremum count, so an empty page starts at 2`,
	"PAGE_FREE": `Offset of the first deleted record available for reuse.
the deleted records are chained through their next pointers
0 means an insert has to take fresh space from PAGE_HEAP_TOP`,
	"PAGE_GARBAGE": `Bytes held by deleted records that have not been reused.
purge and page reorganisation are what give the space back
a page can be full at PAGE_HEAP_TOP while most of it is garbage`,
	"PAGE_LAST_INSERT": `Offset of the record that was inserted last.
with PAGE_DIRECTION it tells InnoDB whether inserts are sequential
a sequential run makes a page split lopsided instead of even`,
	"PAGE_DIRECTION": `Which way the recent inserts have been going.
PAGE_RIGHT means ascending keys, the AUTO_INCREMENT pattern
it resets as soon as an insert breaks the run`,
	"PAGE_N_DIRECTION": `How many inserts in a row have gone the same way.
the longer the run, the more a split favours the growing side
it goes back to zero when PAGE_DIRECTION changes`,
	"PAGE_N_RECS": `Live user records, not counting deleted ones.
PAGE_N_HEAP counts the deleted ones too; the gap is the garbage
infimum and supremum are not counted here`,
	"PAGE_MAX_TRX_ID": `Highest transaction id that changed a record on this page.
only maintained on secondary index pages
it lets a read view skip the page instead of checking every record`,
	"PAGE_LEVEL": `Height above the leaves. 0 is a leaf, the root is the highest.
leaf pages hold the rows, higher levels only route to them
a one-page index is a root that is also a leaf, so level 0`,
	"PAGE_INDEX_ID": `Which index this page belongs to.
the same id appears in the SDI, which is how the index gets a name
pages of different indexes are mixed together in one .ibd`,
	"PAGE_BTR_SEG_LEAF": `File segment that owns the leaf pages of this index.
only set on the root page
the pointer is space, page and offset of an INODE entry`,
	"PAGE_BTR_SEG_TOP": `File segment that owns the non-leaf pages of this index.
only set on the root page
keeping the two apart is what lets leaf pages be read in order`,

	// record header
	"header": `The 5 bytes before a record, laid out backwards from its start.
flags and n_owned, then heap_no and status, then the next pointer
a row written after an instant ADD COLUMN carries a 6th, row_version`,
	"info_bits | n_owned": `One byte: four flag bits and the records this one owns.
delete-marked means purge has not removed it yet
n_owned is non-zero only on a record a directory slot points at`,
	"heap_no | status": `Position in the heap, plus what kind of record this is.
status separates infimum, supremum, node pointer and ordinary
heap_no is insertion order, not key order`,
	"next": `Offset of the next record in key order, relative to this one.
the chain runs from infimum to supremum
it is a delta, so the bytes stay valid when the page is reorganised`,
	"null bitmap": `One bit per nullable column, set when that column is NULL.
it sits before the header, read backwards from the record start
a NULL column stores no bytes at all in the record body`,
	"var lengths": `Stored length of each variable-length column.
one byte when the length fits in 127, otherwise two
the entries run backwards, in column order`,
	"row_version": `Which version of the table definition this row was written with.
only present after an instant ADD or DROP COLUMN
columns added later are read from the SDI default, not from the row`,
	"DB_TRX_ID": `Transaction that last modified this row.
6 bytes, on clustered index records only
a read view compares it to decide whether to follow the undo chain`,
	"DB_ROLL_PTR": `Pointer to the undo record holding the previous version of this row.
7 bytes: rollback segment, page and offset in the undo log
following it is how MVCC rebuilds an older version`,

	// page types
	"INDEX leaf": `A B+tree page at level 0: this is where the rows are.
its neighbours on FIL_PAGE_PREV and NEXT hold the neighbouring keys
a full table scan is this level read from one end to the other`,
	"INDEX node": `A B+tree page above the leaves: keys and the pages they lead to.
each record is a key plus a child page number, no row data
one of these is read per level on the way down to a row`,
	"INDEX": `A B+tree page whose level could not be read.
the INDEX header is there but did not parse
the hex dump is still worth reading`,
	"FIL_PAGE_INDEX": `A B+tree page: INDEX header, records, then the page directory.
the same type carries both leaf and non-leaf pages; PAGE_LEVEL splits them
every index of the table has its own tree of these in the one file`,
	"FIL_PAGE_SDI": `A B+tree page holding the table definition as compressed JSON.
FSP_SDI_ROOT_PAGE_NO on page 0 says where its root is
it is laid out exactly like an INDEX page, with a fixed two-column index`,
	"FIL_PAGE_TYPE_FSP_HDR": `Page 0: the header of the whole tablespace.
size, free limit, flags, the extent lists and the SDI root page
it also carries the extent descriptors for the first 16384 pages`,
	"FIL_PAGE_INODE": `File segment inodes: which extents and pages each segment owns.
a B+tree points here from PAGE_BTR_SEG_LEAF and PAGE_BTR_SEG_TOP
page 2 of a file-per-table space is the first one`,
	"FIL_PAGE_TYPE_XDES": `Extent descriptors: which pages of 64-page extents are free.
one of these every 16384 pages; the first lives inside page 0
allocation reads these before it hands out a page`,
	"FIL_PAGE_TYPE_ALLOCATED": `A page that has been allocated but never written.
it is all zero bytes and has no type of its own yet
the file grows in extents, so fresh space arrives 64 pages at a time`,
	"FIL_PAGE_IBUF_BITMAP": `Two bits per page saying whether change buffering may apply to it.
page 1 of every tablespace, then one every 16384 pages
secondary index maintenance can be deferred through this`,
	"FIL_PAGE_TYPE_BLOB": `Old-format overflow page for a column too long to keep in the row.
the row stores 20 bytes pointing here instead of the value
8.0 tables use the LOB pages instead`,
	"FIL_PAGE_SDI_BLOB": `Overflow page for an SDI record whose JSON does not fit in one page.
the SDI record points at it the way a row points at a BLOB
it is read back and decompressed as one stream`,
	"FIL_PAGE_UNDO_LOG": `Undo records: the previous versions of rows this space changed.
file-per-table spaces do not hold these; undo lives in its own spaces
MVCC reads and rollback both walk them through DB_ROLL_PTR`,
	"FIL_PAGE_TYPE_LOB_FIRST": `First page of an 8.0 large object: its length and index root.
the row points here, and this points at the data pages
partial updates of a LOB are what this format exists for`,
	"FIL_PAGE_TYPE_LOB_INDEX": `Index of the data pages making up one large object.
each entry covers one chunk, in order
an update can replace a chunk without rewriting the whole value`,
	"FIL_PAGE_TYPE_LOB_DATA": `A chunk of a large object.
the bytes are the column value, with no per-page header of their own
the LOB index page says where it belongs in the value`,

	// FSP header
	"FSP_SPACE_ID": `The tablespace id, as it appears in the data dictionary.
every page of the file repeats it in FIL_PAGE_SPACE_ID
redo records name a space by this number, never by file name`,
	"FSP_NOT_USED": `Four bytes that no longer mean anything.
they held the highest page number in use before 5.0
kept so the offsets after them stay where they are`,
	"FSP_SIZE": `Pages in the file, as InnoDB believes it.
compare it with the file size on disk; a smaller file was truncated
the file grows in extents, so this moves in steps of 64`,
	"FSP_FREE_LIMIT": `The first page that has never been initialised.
space below it is described by the extent descriptors
above it the file may exist on disk but has no meaning yet`,
	"FSP_SPACE_FLAGS": `Page size, row format, encryption and whether an SDI is present.
this is what a mismatched page size is detected by
innolens only reads the uncompressed 16 KB combination`,
	"FSP_FRAG_N_USED": `Pages taken out of the fragment extents so far.
the first 32 pages of a table come one at a time, not by the extent
after that an index gets whole extents of its own`,
	"FSP_FREE": `List of extents in which every page is free.
allocation takes from here first
a dropped index gives its extents back to this list`,
	"FSP_FREE_FRAG": `List of extents that are shared between segments and partly used.
FSP_FRAG_N_USED counts the pages taken from them
a small table lives entirely in these`,
	"FSP_FULL_FRAG": `List of shared extents with no free page left.
an extent moves back to FSP_FREE_FRAG when a page is released
nothing can be allocated from here`,
	"FSP_SEG_ID": `The id the next file segment created in this space will get.
it only ever counts up
each B+tree takes two: one for the leaves, one for the rest`,
	"FSP_SEG_INODES_FULL": `List of INODE pages with no free inode slot.
a new segment needs a slot, so it looks at FSP_SEG_INODES_FREE first
one INODE page holds 85 segments at 16 KB`,
	"FSP_SEG_INODES_FREE": `List of INODE pages that still have a free slot.
creating an index takes two slots from here
the list is empty until the first index needs one`,
	"FSP_SDI_VERSION": `Version of the SDI format stored in this tablespace.
it sits just after the FSP header, not inside it
zero means the space carries no SDI`,
	"FSP_SDI_ROOT_PAGE_NO": `Root page of the SDI B+tree.
this is how innolens finds the table definition
page 3 in a freshly created file-per-table space`,

	// segment and list pointers
	"FSEG_HDR_SPACE": `Tablespace of the INODE entry that owns this segment.
it is the same space as the page you are looking at
the three FSEG_HDR fields together address one inode slot`,
	"FSEG_HDR_PAGE_NO": `INODE page holding the entry for this segment.
usually page 2 in a file-per-table space
zero means the segment header was never filled in`,
	"FSEG_HDR_OFFSET": `Byte offset of the entry inside that INODE page.
inode slots are a fixed size, so this picks one of them
with the space and page above it names exactly one segment`,
	"FLST_LEN": `How many nodes are on this list.
zero means the two addresses below it are FIL_NULL
it is maintained on every insert and remove, not counted on demand`,
	"FLST_FIRST": `Address of the first node: page number plus byte offset.
walking the list means following the node pointers from here
FIL_NULL when the list is empty`,
	"FLST_LAST": `Address of the last node on the list.
appending goes here without walking the list
FIL_NULL when the list is empty`,

	// enum values
	"REC_STATUS_ORDINARY": `A leaf record: the key plus the rest of the columns.
on a clustered index that is the whole row, DB_TRX_ID included
on a secondary index it is the indexed columns plus the primary key`,
	"REC_STATUS_NODE_PTR": `A non-leaf record: a key and the child page it leads to.
the child page number is the last 4 bytes of the record
the search compares the key and drops one level`,
	"REC_STATUS_INFIMUM": `The sentinel before every record on the page.
it is where the next chain starts, and it is never returned
it exists so that inserting at the front needs no special case`,
	"REC_STATUS_SUPREMUM": `The sentinel after every record on the page.
reaching it means the scan has to move on to FIL_PAGE_NEXT
like infimum it is created with the page and never moves`,
	"PAGE_RIGHT": `The recent inserts have been going towards higher keys.
this is what an AUTO_INCREMENT primary key produces
a page splitting under it keeps the left side full`,
	"PAGE_LEFT": `The recent inserts have been going towards lower keys.
it happens when rows arrive in descending key order
the split favours the right side instead`,
	"PAGE_SAME_REC": `The last inserts all landed after the same record.
no direction can be inferred from that
splits fall back to the even 50:50 point`,
	"PAGE_SAME_PAGE": `The last inserts stayed on this page without a direction.
same effect as PAGE_SAME_REC for splitting
the counter still runs, but nothing uses it`,
	"PAGE_NO_DIRECTION": `No run of inserts is in progress.
a page that is only read or updated stays here
an insert that breaks a run resets to this`,

	// redo record types
	"MLOG_1BYTE": `Write one byte at an offset in the page.
the generic patch record: whatever InnoDB wrote, byte by byte
counters and flags in a page header arrive this way`,
	"MLOG_2BYTES": `Write two bytes at an offset in the page.
most INDEX header fields are two bytes, so they show up here
the value is stored compressed, not as a plain 2-byte integer`,
	"MLOG_4BYTES": `Write four bytes at an offset in the page.
FIL_PAGE_NEXT and the file list pointers are patched like this
the offset is inside the page, the value is what goes there`,
	"MLOG_8BYTES": `Write eight bytes at an offset in the page.
PAGE_MAX_TRX_ID and segment ids are the usual ones
the same generic patch as the smaller widths, one size up`,
	"MLOG_WRITE_STRING": `Write a run of bytes at an offset in the page.
used when a patch is not one aligned integer
the length comes first, then the bytes exactly as they land`,
	"MLOG_REC_INSERT": `A record was inserted into a page.
it logs the difference from the record the cursor sat on, not the whole row
innolens rebuilds the record from that and decodes the columns`,
	"MLOG_REC_CLUST_DELETE_MARK": `A clustered index record was delete-marked.
the row is still there; purge removes it once no read view needs it
it also logs DB_TRX_ID and DB_ROLL_PTR, so the old version stays reachable`,
	"MLOG_REC_SEC_DELETE_MARK": `A secondary index record was delete-marked.
no transaction id here: secondary records carry none
the record offset is all that is needed to find it again`,
	"MLOG_REC_UPDATE_IN_PLACE": `A record was updated without changing its length.
the update vector lists the changed columns and their new values
if the length changed InnoDB deletes and inserts instead`,
	"MLOG_REC_DELETE": `A record was removed from the page for good.
this is purge or a rollback finishing what delete-mark started
the space goes on the PAGE_FREE list`,
	"MLOG_REC_MIN_MARK": `The min_rec flag was set on a record.
the leftmost record of a non-leaf level carries it
a page split makes a new leftmost record that needs it`,
	"MLOG_LIST_END_DELETE": `Delete every record from one offset to the end of the page.
the second half of a page split, once the records have been copied
one record cannot be deleted this way; it is always a run`,
	"MLOG_LIST_START_DELETE": `Delete every record from the start of the page to one offset.
the mirror image of the previous one
it appears when a split takes the front of the page instead`,
	"MLOG_LIST_END_COPY_CREATED": `Copy a run of records onto a page that was just created.
the other half of a split: the new page gets the tail of the old one
the records are logged as raw bytes, not one insert at a time`,
	"MLOG_PAGE_REORGANIZE": `The records on a page were rewritten in key order.
it reclaims PAGE_GARBAGE without touching the index above
the page comes out with the same records and no free gaps`,
	"MLOG_PAGE_CREATE": `An empty B+tree page was initialised.
it writes the INDEX header, infimum and supremum
a split logs one of these before it copies any record`,
	"MLOG_PAGE_CREATE_RTREE": `An empty R-tree page was initialised.
spatial indexes only; the layout differs from a B+tree page
innolens does not decode the records on one`,
	"MLOG_PAGE_CREATE_SDI": `An empty SDI page was initialised.
the SDI tree is created the same way as any other index
it happens when a table definition is first written`,
	"MLOG_INIT_FILE_PAGE2": `A page was zeroed and given its FIL header.
it carries no body: the page number and space are the whole record
allocating a page from a fresh extent logs this first`,
	"MLOG_IBUF_BITMAP_INIT": `A change buffer bitmap page was initialised.
one is written every 16384 pages as the file grows
it has no body either`,
	"MLOG_UNDO_INSERT": `An undo record was appended to an undo log page.
it holds the previous version of a row that was just changed
DB_ROLL_PTR in the row points at what this wrote`,
	"MLOG_UNDO_INIT": `An undo log page was initialised for insert or update undo.
the two kinds are discarded at different times
insert undo can go as soon as the transaction commits`,
	"MLOG_UNDO_HDR_CREATE": `An undo log header was created for a transaction.
it records the transaction id the log belongs to
one is written the first time a transaction changes a row`,
	"MLOG_UNDO_HDR_REUSE": `An undo log segment was handed to a new transaction.
reusing beats allocating, so committed segments are kept around
the new transaction id is logged in place of the old header`,
	"MLOG_UNDO_ERASE_END": `The tail of an undo log page was erased.
it tidies up after the records that purge has finished with
no body: the page identifies itself`,
	"MLOG_MULTI_REC_END": `End of a multi-record mtr.
everything since the last one is applied together or not at all
a single-record mtr sets a flag on its type byte instead`,
	"MLOG_DUMMY_RECORD": `Padding, written to fill out a log block.
it changes nothing and recovery skips it
seeing a run of them is normal`,
	"MLOG_FILE_CREATE": `A tablespace file was created.
the name and the space flags are logged with it
recovery uses these records to map space ids back to files`,
	"MLOG_FILE_RENAME": `A tablespace file was renamed.
both names are logged, so recovery can follow the move
RENAME TABLE and some ALTER statements produce it`,
	"MLOG_FILE_DELETE": `A tablespace file was dropped.
redo for that space before this point can be ignored
recovery deletes the file again if it is still there`,
	"MLOG_FILE_EXTEND": `A tablespace file grew.
the offset and new size say how far
recovery has to make the file that big before applying later records`,
	"MLOG_TABLE_DYNAMIC_META": `Metadata that belongs to the table rather than to a page.
the AUTO_INCREMENT counter is the one you will actually see
it is written back into the data dictionary at recovery`,
	"MLOG_INDEX_LOAD": `An index was built by sorting instead of by inserting.
the pages were written without redo, so they cannot be recovered
the record exists so that recovery can refuse rather than corrupt`,
	"MLOG_LSN": `A marker carrying the LSN it was written at.
it lets recovery check that it is still in step with the log
it changes nothing on any page`,

	// redo record fields
	"type": `The record type, and whether it is an mtr of its own.
the top bit is MLOG_SINGLE_REC_FLAG: set means no MULTI_REC_END follows
everything after this byte is laid out per type`,
	"space id": `Tablespace the record applies to.
it matches FSP_SPACE_ID on page 0 of that file
stored compressed, so it is one to five bytes`,
	"page no": `Page the record applies to, within that tablespace.
this pair is the whole address; there is no file name in the log
enter on the record opens that page`,
	"index": `The physical layout the record was written against.
column count, which are fixed width, which are nullable
it is here because the log cannot rely on the dictionary at recovery`,
	"index log version": `Format of the index description that follows.
version 1 is what 8.0.30 and later write
anything else means the log came from a version innolens does not read`,
	"flag": `Bits saying whether the row format is compact, versioned or instant.
versioned means the table has been through an instant ADD or DROP COLUMN
redundant row format is not decoded`,
	"n fields": `Number of columns in the logged layout.
it counts DB_TRX_ID and DB_ROLL_PTR on a clustered index
the lengths of each follow, two bytes apiece`,
	"n uniq": `How many leading columns identify a row uniquely.
on a clustered index that is the primary key
the two system columns sit directly after them`,
	"n instant cols": `Columns the table had before the first instant ADD COLUMN.
rows written earlier stop after this many
the rest are filled in from the default in the dictionary`,
	"n instant fields": `Columns whose version numbers are logged individually.
only tables that went through instant ADD or DROP COLUMN have any
each entry says when the column appeared and when it went`,
	"version added": `Table version in which this column appeared.
a row older than it does not store the column at all
row_version in the record header is what it is compared against`,
	"version dropped": `Table version in which this column was dropped.
0 means it is still there
the bytes stay in older rows and are skipped when reading`,
	"cursor rec offset": `Offset of the record the new one was inserted after.
the insert is logged as the difference from it
that is why the log needs so few bytes for a whole row`,
	"end seg len": `Length of the logged tail of the record, with a flag in the low bit.
the flag says whether the record header was logged too
the rest of the record is copied from the cursor record`,
	"info and status bits": `The new record header byte, when it differs from the cursor record.
it carries delete-mark, min_rec and the record status
absent when the two records share it`,
	"origin offset": `Where the columns start inside the rebuilt record.
the variable-length array and null bitmap come before it
it is how innolens knows where to split header from body`,
	"mismatch index": `How many bytes the new record shares with the cursor record.
everything up to here is copied, the rest comes from the log
a long shared prefix is why sequential inserts log so little`,
	"end segment": `The bytes that differ from the cursor record.
put after the copied prefix, this is the whole new record
innolens decodes the columns out of the result`,
	"update vector": `Which columns changed and what they became.
only the changed ones are logged, by position
a column set to NULL is logged with a length of 0xFFFFFFFF`,
	"info bits": `The record header flags to write with the update.
delete-mark is the one that changes here
it is a whole byte even when nothing in it changed`,
	"field no": `Position of the column in the logged layout, not in the table.
DB_TRX_ID and DB_ROLL_PTR are counted, so it can be past the key
the value that follows replaces that column`,
	"sys field pos": `Position of DB_TRX_ID among the columns.
DB_ROLL_PTR is the one after it
the update writes both, so it needs to know where they sit`,
	"rec offset": `Offset of the affected record inside the page.
it points at the columns, not at the record header
the header is the bytes just before it`,
	"flags": `Options the update was made with.
they control locking and dictionary checks during recovery
in a healthy log they are the same on every record`,
	"delete mark": `Whether the record is being marked deleted or unmarked.
rollback of a delete unmarks it, which is the same record type
the row itself is not touched either way`,
	"log data len": `Length of the block of records that follows.
they are logged as raw page bytes, not one insert per record
copying a run in one record is what makes a split cheap`,
	"undo record": `An undo record, as it goes onto the undo page.
it holds the previous version of one row
MVCC and rollback both read it back through DB_ROLL_PTR`,
	"undo page type": `Whether the undo page is for inserts or for updates.
insert undo can be dropped as soon as the transaction commits
update undo has to wait for purge`,
	"trx id": `The transaction the undo log header belongs to.
it is what DB_TRX_ID in a row is compared against
read views decide visibility by comparing these`,
	"table id": `The table the record refers to, as numbered in the dictionary.
not a tablespace id: one table can span several files
dynamic metadata and bulk loads both name a table this way`,
	"index id": `The index the record refers to, as numbered in the dictionary.
the same id is in PAGE_INDEX_ID on every page of that tree
here it names an index reported corrupt`,
	"persistent type": `Which kind of dynamic metadata follows.
1 is a corrupt index, 2 an AUTO_INCREMENT counter
anything else is not decoded`,
	"n indexes": `How many corrupt indexes are listed.
each entry is a space id and an index id
a table with none never writes this record`,
	"autoinc": `The AUTO_INCREMENT counter as it stood.
it is logged so the counter survives a crash
before 8.0 it was recomputed at startup instead`,
	"version": `Version of the table definition the metadata belongs to.
instant ADD and DROP COLUMN move it forward
row_version in a record is matched against it`,
	"fsp flags": `Page size, row format and encryption of the new tablespace.
same layout as FSP_SPACE_FLAGS on page 0
recovery needs it before it can read anything in the file`,
	"file name": `Path of the tablespace file, relative to the datadir.
recovery matches it against what is on disk
the name is how a space id gets back to a file`,
	"from": `Path the tablespace file had before the rename.
the pair lets recovery follow the move
the space id does not change`,
	"to": `Path the tablespace file has after the rename.
if the file is already there, recovery leaves it alone
the space id is unchanged`,
	"offset": `Byte offset in the file the extension started at.
with the size below it says how far the file grew
it is a file offset, not a page number`,
	"size": `Size of the file after it grew.
recovery makes the file at least this big before going on
FSP_SIZE on page 0 is updated separately`,
	"compression level": `zlib level the page was compressed at.
only compressed tablespaces write it
innolens does not read compressed pages`,
	"error": `Where the parser gave up on this record.
the fields before it were read; nothing after it is known
the record length is a guess from that point on`,

	// decoding steps
	"raw": `The bytes as they sit in the page.
everything below is derived from these
comparing them with the value is the point of the tree`,
	"sign bit flipped": `InnoDB stores signed integers with the top bit inverted.
that makes the raw bytes sort in the same order as the values
flipping it back is the first step of decoding one`,
	"bit-packed": `Several fields share the bytes, at fixed bit widths.
DATE packs year, month and day into three bytes this way
the parts are shown before they are formatted`,
	"digit groups": `DECIMAL stores nine decimal digits per four bytes.
the groups are decoded separately and then joined
that is why its size depends on the declared precision`,
	"fraction": `The sub-second part, stored after the whole seconds.
its width comes from the declared precision, 0 to 3 bytes
DATETIME(3) and TIMESTAMP(6) differ only here`,
	"sign": `The sign bit of a DECIMAL, taken from the top of the first byte.
a negative value also has every other bit inverted
it has to come off before the digits mean anything`,
	"unix seconds": `TIMESTAMP is stored as seconds since the epoch, in UTC.
the session time zone is applied when it is displayed
DATETIME is not stored this way, which is the difference between them`,
	"value": `What the bytes above amount to.
this is what a SELECT would return
NULL columns never get here: the null bitmap answers first`,

	// right pane sections
	"mtr": `One mini-transaction: the smallest group of changes applied together.
recovery applies all of its records or none of them
a single statement can produce several`,
	"index tree": `One index of the table, starting from its root page.
expanding a page reads its children then, not before
the leaves are the bottom row of the tree`,
	"Other pages": `Every page in the file that is not part of a B+tree.
page 0, the INODE page, the bitmap and whatever is unallocated
scanning it reads the header of every page in the file`,
	"record image": `The record this redo record inserted, rebuilt from the log.
the log holds only the difference from the record before it
column names come from the SDI when the tablespace is at hand`,

	// undo
	"undo tablespace": `Undo logs: the previous version of every row a transaction changed.
DB_ROLL_PTR in a clustered index record points into one of these
rollback reads them backwards, MVCC reads them to see an older row`,
	"undo page header": `The 18 bytes telling what this undo page holds and how full it is.
insert undo is thrown away at commit, update undo waits for purge
the file list node chains the pages of one undo segment`,
	"undo segment header": `Only on the first page of a segment: its state and page list.
a segment belongs to one transaction at a time and is then reused
the file segment header owns the extents the segment grew into`,
	"undo log header": `One transaction's slot in the segment: its id and where its records start.
several can sit on the same page, chained by TRX_UNDO_NEXT_LOG
after commit it moves to the history list, which purge walks`,
	"TRX_UNDO_PAGE_TYPE": `Whether this page holds insert undo or update undo.
insert undo only has to name the row, so a rollback can delete it
update undo carries the old column values as well`,
	"TRX_UNDO_INSERT": `Undo for rows this transaction inserted.
rolling back means deleting them, so the old values are not needed
the whole segment is discarded at commit`,
	"TRX_UNDO_UPDATE": `Undo for rows this transaction updated or delete-marked.
it holds the old values, which is what MVCC reads to see an older row
it stays until purge decides no read view still needs it`,
	"TRX_UNDO_PAGE_START": `Offset of the first undo record on this page.
on the first page of a segment the log headers come before it
purge moves it forward as it removes records from the front`,
	"TRX_UNDO_PAGE_FREE": `Offset where the next undo record would be written.
everything between PAGE_START and here is records
a record is appended at this offset and this is then moved past it`,
	"TRX_UNDO_PAGE_NODE": `The list node chaining this page into its undo segment.
the base node of that list is TRX_UNDO_PAGE_LIST on the first page
undo records are read across pages by following it`,
	"TRX_UNDO_STATE": `What the segment is doing: active, cached for reuse, or waiting.
ACTIVE means a transaction is still writing to it
TO_PURGE means it is committed and purge has yet to free it`,
	"TRX_UNDO_ACTIVE": `A transaction is still writing undo into this segment.
it has neither committed nor rolled back
recovery rolls back whatever it finds in this state`,
	"TRX_UNDO_CACHED": `The segment is free and kept for the next transaction.
reusing it saves allocating pages for every transaction
the next transaction writes a new log header into it`,
	"TRX_UNDO_TO_FREE": `Insert undo that can be thrown away: the transaction is over.
nothing needs the old values, since the rows were new
the pages go back to the tablespace`,
	"TRX_UNDO_TO_PURGE": `Update undo waiting for purge to be done with it.
it stays reachable while any read view might still need the old rows
purge frees it once the history list has moved past it`,
	"TRX_UNDO_LAST_LOG": `Offset of the newest undo log header on this page, 0 if none.
a segment is handed to one transaction after another
each gets a header, chained back from here`,
	"TRX_UNDO_FSEG_HEADER": `The file segment this undo segment allocates its pages from.
it points at the INODE entry that owns the extents
the same 10-byte shape as the B+tree segment headers`,
	"TRX_UNDO_PAGE_LIST": `Base node of the list of pages making up this undo segment.
each page chains into it through TRX_UNDO_PAGE_NODE
a long undo log is read by walking this list`,
	"TRX_UNDO_TRX_ID": `The transaction that owns this undo log.
rows written by it carry the same id in DB_TRX_ID
a read view compares against it to decide what it may see`,
	"TRX_UNDO_TRX_NO": `The commit order of the transaction, set when it commits.
purge works through the history list in this order
it is 0 while the transaction is still running`,
	"TRX_UNDO_DEL_MARKS": `Whether this transaction delete-marked anything.
if it did, purge has to visit the secondary indexes as well
a plain update leaves it false`,
	"TRX_UNDO_LOG_START": `Offset of the first undo record of this log on the page.
purge removes records from the front, so it is not the header end
the records of one transaction start here`,
	"TRX_UNDO_FLAGS": `One byte of flags: whether an XID or GTID follows the header.
XA transactions store their identifier after the fixed fields
replication stores the GTID there once the transaction commits`,
	"TRX_UNDO_DICT_TRANS": `Set when the transaction created or dropped a table.
recovery cannot undo that record by record
it drops the half-created object instead`,
	"TRX_UNDO_TABLE_ID": `The table of a dictionary transaction. Deprecated otherwise.
each undo record names its own table id anyway
it is left over from the days of a single dictionary`,
	"TRX_UNDO_NEXT_LOG": `Offset of the next undo log header on this page, 0 if none.
a reused segment stacks the headers of the transactions it served
this is how they are walked`,
	"TRX_UNDO_PREV_LOG": `Offset of the previous undo log header on this page, 0 if none.
the other direction of the same chain
TRX_UNDO_LAST_LOG names the end of it`,
	"TRX_UNDO_HISTORY_NODE": `The list node putting this log into the rollback segment history.
purge walks that list in commit order
it is set at commit, not while the transaction runs`,

	// undo record types and fields
	"TRX_UNDO_INSERT_REC": `Undo for one inserted row: just enough to find and delete it.
only the primary key is stored, because there is no older version
a rollback removes the row the key names`,
	"TRX_UNDO_UPD_EXIST_REC": `Undo for one updated row: the values the update replaced.
the primary key finds the row, the update vector holds the old values
this is what an MVCC read applies to see the row as it was`,
	"TRX_UNDO_UPD_DEL_REC": `Undo for updating a row whose slot was delete-marked.
an insert reuses a delete-marked record instead of taking new space
the old values are those of the deleted row`,
	"TRX_UNDO_DEL_MARK_REC": `Undo for delete-marking one row. The columns do not change.
a DELETE only sets the flag; purge removes the record later
undoing it clears the flag again`,
	"next record offset": `Where the next undo record on this page starts.
records are appended, so this points forward to the free end
the last two bytes of a record point back at its own start`,
	"type_cmpl": `The kind of change this record undoes, plus flags.
the low bits are the type, the next two say what a secondary index needs
the top bits mark off-page columns and the newer BLOB undo format`,
	"undo_rec_flags": `A byte reserved by the BLOB-aware undo format.
it is zero today; the format keeps room to extend
present whenever the blob_undo bit is set in type_cmpl`,
	"undo_no": `The position of this record within its transaction, counting from 0.
a rollback applies them in reverse
purge and MVCC use it to stop at the right point`,
	"table_id": `The table the changed row belongs to.
a reader compares it before trusting the columns
a rebuilt table gets a new id, which is how stale undo is spotted`,
	"info_bits": `The record header flags of the row before the change.
undoing a delete-mark means putting these back
the delete bit here says whether the row was already marked`,
	"key fields": `The primary key of the row this record undoes.
it is what finds the row again in the clustered index
secondary index entries are found from the columns below`,
	"old values": `The columns the update replaced, each with its position.
only changed columns are stored, which is why undo stays small
applying them to the current row rebuilds the older version`,
	"index columns": `The old values of every column that orders some index.
purge needs them to find the matching secondary index entries
written for a delete-mark, or when an update moved an index entry`,
	"n_fields": `How many columns the update vector holds.
the positions that follow are positions in the clustered index
an update of one column stores one entry`,
	"field_no": `Which column of the clustered index the old value belongs to.
with instant ADD or DROP COLUMN it is the physical position
DB_TRX_ID and DB_ROLL_PTR are counted, so it can be past the key`,
	"columns": `The columns of this record were not decoded.
an undo record does not name its table, only a table id
reaching it from a row's DB_ROLL_PTR supplies the definition`,
}

// helpFor is the entry for the selected row, or the nearest ancestor that has
// one. The name returned is the key it was found under, so a row that fell back
// to its parent says so.
func (l *list) helpFor() (name, body string) {
	if l.cur >= len(l.rows) {
		return "", ""
	}
	depth := l.rows[l.cur].depth + 1
	for i := l.cur; i >= 0; i-- {
		if l.rows[i].depth >= depth {
			continue
		}
		depth = l.rows[i].depth
		for _, k := range helpKeys(l.rows[i].n) {
			if body, ok := help[k]; ok {
				return k, body
			}
		}
	}
	return "", ""
}

// helpKeys are the keys a row is looked up by, most specific first: the constant
// an enum field decoded to, then the explicit key, the annotation field name and
// the type token the row is coloured by.
func helpKeys(n *node) []string {
	var keys []string
	if k := enumKey(n.value); k != "" {
		keys = append(keys, k)
	}
	if n.hkey != "" {
		keys = append(keys, n.hkey)
	}
	if in, ok := n.data.(*innodb.Node); ok {
		keys = append(keys, in.Name)
	}
	if n.tag != "" {
		keys = append(keys, n.tag)
	}
	return keys
}

// enumKey is the token in the last parentheses of a value, which is where the
// decoders put the name a number stands for.
func enumKey(v string) string {
	if !strings.HasSuffix(v, ")") {
		return ""
	}
	i := strings.LastIndexByte(v, '(')
	if i < 0 {
		return ""
	}
	return v[i+1 : len(v)-1]
}

// helpPanel is the ? panel: always helpLines tall so the panes above it do not
// move as the cursor does.
func (m *Model) helpPanel() []string {
	name, body := m.cur().helpFor()
	lines := make([]string, 0, helpLines)
	if name == "" {
		lines = append(lines, dimStyle.Render("no description"))
	} else {
		for i, part := range strings.SplitN(body, "\n", helpLines) {
			if i == 0 {
				lines = append(lines, headLine(name, part, m.w))
				continue
			}
			lines = append(lines, dimStyle.Render(truncate("  · "+part, m.w)))
		}
	}
	for len(lines) < helpLines {
		lines = append(lines, "")
	}
	return lines
}

// headLine is "NAME  summary" with the name picked out, styled after truncation
// so that a narrow terminal does not cut an escape sequence in half.
func headLine(name, summary string, w int) string {
	text := truncate(name+"  "+summary, w)
	if !strings.HasPrefix(text, name) {
		return text
	}
	return headStyle.Render(name) + text[len(name):]
}
